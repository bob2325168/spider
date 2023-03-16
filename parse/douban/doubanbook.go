package douban

import (
	"github.com/bob2325168/spider/collect"
	"go.uber.org/zap"
	"regexp"
	"strconv"
)

var DoubanBookTask = &collect.Task{
	Property: collect.Property{
		Name:     "douban_book_list",
		WaitTime: 2,
		MaxDepth: 5,
		Cookie: "bid=-UXUw--yL5g; dbcl2=\"214281202:q0BBm9YC2Yg\"; __yadk_uid=jigAbrEOKiwgbAaLUt0G3yPsvehXcvrs; " +
			"push_noty_num=0; push_doumail_num=0; __utmz=30149280.1665849857.1.1.utmcsr=accounts.douban.com|utmccn=(referral)|utmcmd=referral|utmcct=/; " +
			"__utmv=30149280.21428; ck=SAvm; _pk_ref.100001.8cb4=%5B%22%22%2C%22%22%2C1665925405%2C%22https%3A%2F%2Faccounts.douban.com%2F%22%5D; _pk_ses.100001.8cb4=*; " +
			"__utma=30149280.2072705865.1665849857.1665849857.1665925407.2; __utmc=30149280; __utmt=1; __utmb=30149280.23.5.1665925419338;" +
			" _pk_id.100001.8cb4=fc1581490bf2b70c.1665849856.2.1665925421.1665849856.",
	},
	Rule: collect.RuleTree{
		Root: func() ([]*collect.Request, error) {
			roots := []*collect.Request{
				{
					Priority: 1,
					URL:      "https://book.douban.com",
					Method:   "GET",
					RuleName: "数据tag",
				},
			}
			return roots, nil
		},
		Trunk: map[string]*collect.Rule{
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

func ParseTag(ctx *collect.Context) (collect.ParseResult, error) {
	re := regexp.MustCompile(regexpStr)

	matches := re.FindAllSubmatch(ctx.Body, -1)
	result := collect.ParseResult{}

	for _, m := range matches {
		result.Requests = append(
			result.Requests, &collect.Request{
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

func ParseBookList(ctx *collect.Context) (collect.ParseResult, error) {
	re := regexp.MustCompile(BooklistRe)
	matches := re.FindAllSubmatch(ctx.Body, -1)
	result := collect.ParseResult{}
	for _, m := range matches {
		req := &collect.Request{
			Method:   "GET",
			Task:     ctx.Req.Task,
			URL:      string(m[1]),
			Depth:    ctx.Req.Depth + 1,
			RuleName: "书籍简介",
			Priority: 100,
		}
		req.TmpData = &collect.Temp{}
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

func ParseBookDetail(ctx *collect.Context) (collect.ParseResult, error) {
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

	result := collect.ParseResult{
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
