package sqlstorage

import (
	"github.com/bob2325168/spider/engine"
	"github.com/bob2325168/spider/parse/douban"
	"github.com/bob2325168/spider/parse/doubangroup"
	"github.com/bob2325168/spider/parse/doubangroupjs"
	"github.com/bob2325168/spider/spider"
	"github.com/bob2325168/spider/sqldb"
	"github.com/stretchr/testify/assert"
	"testing"
)

func init() {
	engine.Store.Add(doubangroup.GroupTask)
	engine.Store.Add(douban.BookTask)
	engine.Store.AddJsTask(doubangroupjs.GroupJSTask)
}

type mysqldb struct {
}

func (m mysqldb) CreateTable(t sqldb.TableData) error {
	return nil
}

func (m mysqldb) Insert(t sqldb.TableData) error {
	return nil
}

func TestSQLStorage_Flush(t *testing.T) {

	type fields struct {
		dataCell []*spider.DataCell
		options  options
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{name: "empty", wantErr: false},

		{name: "no Rule filed", fields: fields{dataCell: []*spider.DataCell{
			{Data: map[string]interface{}{"url": "http://xxx.com"}},
		}}, wantErr: true},

		{name: "no Task filed", fields: fields{dataCell: []*spider.DataCell{
			{Data: map[string]interface{}{"url": "http://xxx.com"}},
		}}, wantErr: true},

		{name: "right data", fields: fields{dataCell: []*spider.DataCell{
			{Data: map[string]interface{}{"Rule": "书籍简介", "Task": "douban_book_list", "Data": map[string]interface{}{"url": "http://xxx.com"}}},
		}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SqlStore{
				dataCell: tt.fields.dataCell,
				db:       mysqldb{},
				options:  tt.fields.options,
			}

			if err := s.Flush(); (err != nil) != tt.wantErr {
				t.Errorf("Flush() error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Nil(t, s.dataCell)
		})
	}
}
