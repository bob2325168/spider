package douban

import (
	"github.com/bob2325168/spider/middlewares/limiter"
	"github.com/bob2325168/spider/spider"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"regexp"
	"strconv"
	"time"
)

var BookTask = &spider.Task{
	Options: spider.Options{
		Name:     "douban_book_list",
		WaitTime: 2,
		MaxDepth: 5,
		Cookie:   "__Secure-3PAPISID=syRZh49k3Ly4GbH3/AOnbR3RsAI0yA8PDW; __Secure-3PSID=Twhd6jxVkHKO2ttDC0GLtozGuZdvukYa8gfvQ60Z38cf_DgXtxhCGUNKbE0uZnqBJp9INw.; 1P_JAR=2023-03-16-07; NID=511=nY--paEemdRdrxnDh45vRVS9CwdCxWrAB_WcGd881Noe38RsZgIe6O0alR-37fe7S1BmOGQhtUtBOjrlfdwJqD9GCobLvAdAZyHarnyVHoF9bITePzu_z2tgdu-82nVgorFtsksZ7lrJaS6pVljyG3wuyE-LWmV3rFnHns8cqgvEz0xnwTwgNsb-bUoJtXxHEc4kSyThUcIzPSgAIbO06Ae8-3RlViW5HT1dYKkHI1mIG-z1m9bIZNj_-NwRYljwZQs3YtOsN1xiJtFe4aXtNXiLc-1JNcHl2lHncjIRzuzrO7rTkqNwrUXJ; __Secure-3PSIDCC=AFvIBn9ifnhgfsJGfVreP-uldS_QSO5ydc1vRUCNQO4xgJgVl6UwYrCbK-CJVpXThvRKf7q3FA",
		Reload:   true,
		Limiter: limiter.Multi(
			rate.NewLimiter(limiter.Per(1, 3*time.Second), 1),
			rate.NewLimiter(limiter.Per(20, 60*time.Second), 20),
		),
	},
	Rule: spider.RuleTree{
		Root: func() ([]*spider.Request, error) {
			roots := []*spider.Request{
				{
					Priority: 1,
					URL:      "https://book.douban.com",
					Method:   "GET",
					RuleName: "数据tag",
				},
			}
			return roots, nil
		},
		Trunk: map[string]*spider.Rule{
			"数据tag": {ParseFunc: ParseTag},
			"书籍列表":  {ParseFunc: ParseBookList},
			"书籍简介": {
				ItemFields: []string{
					"book_name",
					"author",
					"page",
					"press",
					"score",
					"price",
					"description",
				},
				ParseFunc: ParseBookDetail,
			},
		},
	},
}

const regexpStr = `<a href="([^"]+)" class="tag">([^<]+)</a>`

func ParseTag(ctx *spider.Context) (spider.ParseResult, error) {
	re := regexp.MustCompile(regexpStr)

	matches := re.FindAllSubmatch(ctx.Body, -1)
	result := spider.ParseResult{}

	for _, m := range matches {
		result.Requests = append(
			result.Requests, &spider.Request{
				Method:   "GET",
				Task:     ctx.Req.Task,
				URL:      "https://book.douban.com" + string(m[1]),
				Depth:    ctx.Req.Depth + 1,
				RuleName: "书籍列表",
			})
	}
	zap.S().Debug("parse book tag.count", len(result.Requests))
	return result, nil
}

const BooklistRe = `<a.*?href="([^"]+)" title="([^"]+)"`

func ParseBookList(ctx *spider.Context) (spider.ParseResult, error) {
	re := regexp.MustCompile(BooklistRe)
	matches := re.FindAllSubmatch(ctx.Body, -1)
	result := spider.ParseResult{}
	for _, m := range matches {
		req := &spider.Request{
			Method:   "GET",
			Task:     ctx.Req.Task,
			URL:      string(m[1]),
			Depth:    ctx.Req.Depth + 1,
			RuleName: "书籍简介",
			Priority: 100,
		}
		req.TmpData = &spider.Temp{}
		if err := req.TmpData.Set("book_name", string(m[2])); err != nil {
			zap.L().Error("set temdata failed", zap.Error(err))
		}
		result.Requests = append(result.Requests, req)
	}
	zap.S().Debug("parse book list.count", len(result.Requests))
	return result, nil
}

var autoRe = regexp.MustCompile(`<span class="pl"> 作者</span>:[\d\D]*?<a.*?>([^<]+)</a>`)
var public = regexp.MustCompile(`<span class="pl">出版社:</span>[\d\D]*?<a.*?>([^<]+)</a>`)
var pageRe = regexp.MustCompile(`<span class="pl">页数:</span> ([^<]+)<br/>`)
var priceRe = regexp.MustCompile(`<span class="pl">定价:</span>([^<]+)<br/>`)
var scoreRe = regexp.MustCompile(`<strong class="ll rating_num " property="v:average">([^<]+)</strong>`)
var intoRe = regexp.MustCompile(`<div class="intro">[\d\D]*?<p>([^<]+)</p></div>`)

func ParseBookDetail(ctx *spider.Context) (spider.ParseResult, error) {
	bookName := ctx.Req.TmpData.Get("book_name")
	page, _ := strconv.Atoi(ExtraString(ctx.Body, pageRe))

	book := map[string]interface{}{
		"book_name":   bookName,
		"author":      ExtraString(ctx.Body, autoRe),
		"page":        page,
		"press":       ExtraString(ctx.Body, public),
		"score":       ExtraString(ctx.Body, scoreRe),
		"price":       ExtraString(ctx.Body, priceRe),
		"description": ExtraString(ctx.Body, intoRe),
	}
	data := ctx.Output(book)

	result := spider.ParseResult{
		Items: []interface{}{data},
	}
	zap.S().Debugln("parse book detail", data)
	return result, nil
}

func ExtraString(contents []byte, re *regexp.Regexp) string {

	match := re.FindSubmatch(contents)

	if len(match) >= 2 {
		return string(match[1])
	}

	return ""
}
