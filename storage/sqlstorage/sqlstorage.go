package sqlstorage

import (
	"encoding/json"
	"fmt"
	"github.com/bob2325168/spider/engine"
	"github.com/bob2325168/spider/sqldb"
	"github.com/bob2325168/spider/storage"
	"go.uber.org/zap"
)

type SqlStore struct {
	dataCell    []*storage.DataCell
	columnNames []sqldb.Field
	db          sqldb.DBer
	Table       map[string]struct{}

	options
}

func NewSqlStore(opts ...Option) (*SqlStore, error) {

	option := defaultOption
	for _, opt := range opts {
		opt(&option)
	}
	s := &SqlStore{}
	s.options = option
	s.Table = make(map[string]struct{})
	var err error
	s.db, err = sqldb.New(
		sqldb.WithLogger(s.logger),
		sqldb.WithConnUrl(s.sqlURL),
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SqlStore) Save(dataCells ...*storage.DataCell) error {

	for _, cell := range dataCells {
		tableName := cell.GetTableName()
		if _, ok := s.Table[tableName]; !ok {
			//创建表
			columnNames := getFields(cell)
			err := s.db.CreateTable(sqldb.TableData{
				TableName:   tableName,
				ColumnNames: columnNames,
				AutoKey:     true,
			})
			if err != nil {
				s.logger.Error("create table failed", zap.Error(err))
				return err
			}
			s.Table[tableName] = struct{}{}
		}
		if len(s.dataCell) >= s.BatchCount {
			s.Flush()
		}
		s.dataCell = append(s.dataCell, cell)
	}
	return nil
}

func getFields(cell *storage.DataCell) []sqldb.Field {

	taskName := cell.Data["Task"].(string)
	ruleName := cell.Data["Rule"].(string)
	fields := engine.GetFields(taskName, ruleName)

	var columnNames []sqldb.Field
	for _, field := range fields {
		columnNames = append(columnNames, sqldb.Field{
			Title: field,
			Type:  "MEDIUMTEXT",
		})
	}
	columnNames = append(columnNames,
		sqldb.Field{Title: "URL", Type: "VARCHAR(255)"},
		sqldb.Field{Title: "Time", Type: "VARCHAR(255)"},
	)
	return columnNames
}

func (s *SqlStore) Flush() error {

	s.logger.Info(fmt.Sprintf("flushing data: %d", len(s.dataCell)))
	if len(s.dataCell) == 0 {
		return nil
	}
	args := make([]interface{}, 0)
	for _, datacell := range s.dataCell {
		ruleName := datacell.Data["Rule"].(string)
		taskName := datacell.Data["Task"].(string)
		fields := engine.GetFields(taskName, ruleName)
		data := datacell.Data["Data"].(map[string]interface{})
		var value []string
		for _, field := range fields {
			v := data[field]
			switch v := v.(type) {
			case nil:
				value = append(value, "")
			case string:
				value = append(value, v)
			default:
				j, err := json.Marshal(v)
				if err != nil {
					value = append(value, "")
				} else {
					value = append(value, string(j))
				}
			}
		}
		value = append(value, datacell.Data["URL"].(string), datacell.Data["Time"].(string))
		for _, v := range value {
			args = append(args, v)
		}
	}

	return s.db.Insert(sqldb.TableData{
		TableName:   s.dataCell[0].GetTableName(),
		ColumnNames: getFields(s.dataCell[0]),
		Args:        args,
		DataCount:   len(s.dataCell),
	})
}
