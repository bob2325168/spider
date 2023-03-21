package sqlstorage

import (
	"encoding/json"
	"errors"
	"github.com/bob2325168/spider/engine"
	"github.com/bob2325168/spider/spider"
	"github.com/bob2325168/spider/sqldb"
	"go.uber.org/zap"
)

type SqlStore struct {
	// 缓存输出的结果
	dataCell []*spider.DataCell
	db       sqldb.DBer
	Table    map[string]struct{}

	options
}

func New(opts ...Option) (*SqlStore, error) {

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

func (s *SqlStore) Save(dataCells ...*spider.DataCell) error {

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
				continue
			}
			s.Table[tableName] = struct{}{}
		}
		if len(s.dataCell) >= s.BatchCount {
			if err := s.Flush(); err != nil {
				s.logger.Error("insert data failed", zap.Error(err))
			}
		}
		s.dataCell = append(s.dataCell, cell)
	}
	return nil
}

func getFields(cell *spider.DataCell) []sqldb.Field {

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

	if len(s.dataCell) == 0 {
		return nil
	}

	args := make([]interface{}, 0)
	var ruleName string
	var taskName string
	var ok bool

	for _, cell := range s.dataCell {
		if ruleName, ok = cell.Data["Rule"].(string); !ok {
			return errors.New("no rule field")
		}
		if taskName, ok = cell.Data["Task"].(string); !ok {
			return errors.New("no task field")
		}
		fields := engine.GetFields(taskName, ruleName)

		data := cell.Data["Data"].(map[string]interface{})
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
		if v, ok := cell.Data["URL"].(string); ok {
			value = append(value, v)
		}
		if v, ok := cell.Data["Time"].(string); ok {
			value = append(value, v)
		}
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
