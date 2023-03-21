package spider

import (
	"sync"
)

type Fetcher interface {
	Get(url *Request) ([]byte, error)
}

// Task 一个任务实例
type Task struct {
	Visited     map[string]bool //是否爬过该网站
	VisitedLock sync.Mutex
	Rule        RuleTree
	Closed      bool // 用于标识任务已经删除

	Options
}

type Property struct {
	Name     string `json:"name"` // 任务名称，是全局唯一的
	Url      string `json:"url"`
	Cookie   string `json:"cookie"`
	WaitTime int64  `json:"wait_time"` // 随机休眠时间，单位是秒
	MaxDepth int64  `json:"max_depth"` // 爬取的最大深度
	Reload   bool   `json:"reload"`    // 网站是否可以重复爬取
}

type TaskConfig struct {
	Name     string
	Cookie   string
	WaitTime int64
	Reload   bool
	MaxDepth int64
	Fetcher  string
	Limits   []LimitConfig
}

type LimitConfig struct {
	EventCount int // 数量
	EventDur   int // 秒
	Bucket     int // 桶大小
}

func NewTask(opts ...Option) *Task {
	options := defaultOptions
	for _, opt := range opts {
		opt(&options)
	}
	t := &Task{}
	t.Options = options
	return t
}
