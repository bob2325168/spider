package limiter

import (
	"golang.org/x/net/context"
	"sort"
	"time"

	"golang.org/x/time/rate"
)

type MultiLimiter struct {
	limiters []RateLimiter
}

type RateLimiter interface {
	Wait(context.Context) error
	Limit() rate.Limit
}

func Per(eventCount int, duration time.Duration) rate.Limit {
	return rate.Every(duration / time.Duration(eventCount))
}

func Multi(limiters ...RateLimiter) *MultiLimiter {

	byLimit := func(i, j int) bool {
		return limiters[i].Limit() < limiters[j].Limit()
	}
	sort.Slice(limiters, byLimit)
	return &MultiLimiter{limiters: limiters}
}

func (l *MultiLimiter) Wait(ctx context.Context) error {
	for _, l := range l.limiters {
		if err := l.Wait(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (l *MultiLimiter) Limit() rate.Limit {
	return l.limiters[0].Limit()
}
