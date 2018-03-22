package elasticsearch

import (
	"context"

	"fmt"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/inloco/kafka-elasticsearch-injector/src/models"
	"github.com/olivere/elastic"
	"github.com/pkg/errors"
)

var esClient *elastic.Client

type basicDatabase interface {
	GetClient() *elastic.Client
	CloseClient()
}

type RecordDatabase interface {
	basicDatabase
	Insert(records []*models.Record) error
	ReadinessCheck() bool
}

type recordDatabase struct {
	logger log.Logger
	config Config
}

func (d recordDatabase) GetClient() *elastic.Client {
	if esClient == nil {
		client, err := elastic.NewClient(elastic.SetURL(d.config.Host))
		if err != nil {
			level.Error(d.logger).Log("err", err, "message", "could not init elasticsearch client")
			panic(err)
		}
		esClient = client
	}
	return esClient
}

func (d recordDatabase) CloseClient() {
	if esClient != nil {
		esClient.Stop()
		esClient = nil
	}
}

func (d recordDatabase) Insert(records []*models.Record) error {
	bulkRequest, err := d.buildBulkRequest(records)
	if err != nil {
		return err
	}
	timeout := d.config.BulkTimeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := bulkRequest.Do(ctx)
	if err == nil {
		if res.Errors {
			for _, f := range res.Failed() {
				return errors.New(fmt.Sprintf("%v", f.Error))
			}
		}
	}

	return err
}

func (d recordDatabase) ReadinessCheck() bool {
	info, _, err := d.GetClient().Ping(d.config.Host).Do(context.Background())
	if err != nil {
		level.Error(d.logger).Log("err", err, "message", "error pinging elasticsearch")
		return false
	}
	level.Info(d.logger).Log("message", fmt.Sprintf("connected to es version %s", info.Version.Number))
	return true
}

func (d recordDatabase) buildBulkRequest(records []*models.Record) (*elastic.BulkService, error) {
	bulkRequest := d.GetClient().Bulk()
	for _, record := range records {
		index, err := d.getDatabaseIndex(record)
		if err != nil {
			return nil, err
		}

		docID, err := d.getDatabaseDocID(record)
		if err != nil {
			return nil, err
		}

		bulkRequest.Add(elastic.NewBulkIndexRequest().Index(index).
			Type(record.Topic).
			Id(docID).
			Doc(record.FilteredFieldsJSON(d.config.BlacklistedColumns)))
	}
	return bulkRequest, nil
}

func (d recordDatabase) getDatabaseIndex(record *models.Record) (string, error) {
	indexPrefix := d.config.Index
	if indexPrefix == "" {
		indexPrefix = record.Topic
	}

	indexColumn := d.config.IndexColumn
	indexSuffix := record.FormatTimestamp()
	if indexColumn != "" {
		newIndexSuffix, err := record.GetValueForField(indexColumn)
		if err != nil {
			level.Error(d.logger).Log("err", err, "message", "Could not get column value from record.")
			return "", err
		}
		indexSuffix = newIndexSuffix
	}

	return fmt.Sprintf("%s-%s", indexPrefix, indexSuffix), nil
}

func (d recordDatabase) getDatabaseDocID(record *models.Record) (string, error) {
	docID := record.GetId()

	docIDColumn := d.config.DocIDColumn
	if docIDColumn != "" {
		newDocID, err := record.GetValueForField(docIDColumn)
		if err != nil {
			level.Error(d.logger).Log("err", err, "message", "Could not get doc id value from record.")
			return "", err
		}
		docID = newDocID
	}
	return docID, nil
}

func NewDatabase(logger log.Logger, config Config) RecordDatabase {
	return recordDatabase{logger: logger, config: config}
}
