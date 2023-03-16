package doubangroupjs

import "github.com/bob2325168/spider/spider"

var GroupJSTask = &spider.TaskModule{
	Property: spider.Property{
		Name:     "js_find_douban_sun_room",
		WaitTime: 2,
		MaxDepth: 5,
		Cookie:   "__Secure-3PAPISID=syRZh49k3Ly4GbH3/AOnbR3RsAI0yA8PDW; __Secure-3PSID=Twhd6jxVkHKO2ttDC0GLtozGuZdvukYa8gfvQ60Z38cf_DgXtxhCGUNKbE0uZnqBJp9INw.; 1P_JAR=2023-03-16-07; NID=511=nY--paEemdRdrxnDh45vRVS9CwdCxWrAB_WcGd881Noe38RsZgIe6O0alR-37fe7S1BmOGQhtUtBOjrlfdwJqD9GCobLvAdAZyHarnyVHoF9bITePzu_z2tgdu-82nVgorFtsksZ7lrJaS6pVljyG3wuyE-LWmV3rFnHns8cqgvEz0xnwTwgNsb-bUoJtXxHEc4kSyThUcIzPSgAIbO06Ae8-3RlViW5HT1dYKkHI1mIG-z1m9bIZNj_-NwRYljwZQs3YtOsN1xiJtFe4aXtNXiLc-1JNcHl2lHncjIRzuzrO7rTkqNwrUXJ; __Secure-3PSIDCC=AFvIBn9ifnhgfsJGfVreP-uldS_QSO5ydc1vRUCNQO4xgJgVl6UwYrCbK-CJVpXThvRKf7q3FA",
	},
	Root: `
		var arr = new Array();
 		for (var i = 25; i <= 25; i+=25) {
			var obj = {
			   URL: "https://www.douban.com/group/szsh/discussion?start=" + i,
			   Priority: 1,
			   RuleName: "解析网站URL",
			   Method: "GET",
		   };
			arr.push(obj);
		};
		console.log(arr[0].URL);
		AddJsReq(arr);
			`,
	Rules: []spider.RuleModule{
		{
			Name: "解析网站URL",
			ParseFunc: `
			ctx.ParseJSReg("解析阳台房","(https://www.douban.com/group/topic/[0-9a-z]+/)\"[^>]*>([^<]+)</a>");
			`,
		},
		{
			Name: "解析阳台房",
			ParseFunc: `
			//console.log("parse output");
			ctx.OutputJS("<div class=\"topic-content\">[\\s\\S]*?阳台[\\s\\S]*?<div class=\"aside\">");
			`,
		},
	},
}
