package engine

import (
	"github.com/bob2325168/spider/spider"
	"go.uber.org/zap"
)

type Option func(opts *options)

type options struct {
	WorkCount   int
	Fetcher     spider.Fetcher
	Logger      *zap.Logger
	Seeds       []*spider.Task
	Storage     spider.Storage
	RegistryUrl string

	scheduler Scheduler
}

var defaultOptions = options{
	Logger: zap.NewNop(),
}

func WithLogger(logger *zap.Logger) Option {
	return func(opts *options) {
		opts.Logger = logger
	}
}

func WithFetcher(fetcher spider.Fetcher) Option {
	return func(opts *options) {
		opts.Fetcher = fetcher
	}
}

func WithWorkCount(workCount int) Option {
	return func(opts *options) {
		opts.WorkCount = workCount
	}
}

func WithSeeds(seed []*spider.Task) Option {
	return func(opts *options) {
		opts.Seeds = seed
	}
}

func WithScheduler(scheduler Scheduler) Option {
	return func(opts *options) {
		opts.scheduler = scheduler
	}
}

func WithStorage(s spider.Storage) Option {
	return func(opts *options) {
		opts.Storage = s
	}
}

func WithRegistryUrl(url string) Option {
	return func(opts *options) {
		opts.RegistryUrl = url
	}
}
