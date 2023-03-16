package sqlstorage

import "go.uber.org/zap"

type options struct {
	logger     *zap.Logger
	sqlURL     string
	BatchCount int
}

type Option func(opts *options)

var defaultOption = options{logger: zap.NewNop()}

func WithLogger(logger *zap.Logger) Option {
	return func(opts *options) {
		opts.logger = logger
	}
}

func WithSqlURL(sqlURL string) Option {
	return func(opts *options) {
		opts.sqlURL = sqlURL
	}
}

func WithBatchCount(batchCount int) Option {
	return func(opts *options) {
		opts.BatchCount = batchCount
	}
}
