package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/neouks/series/db"
)

// 真实台账条目（潍坊银行综合积分权益平台）的字段长度：框架 76、业务 53、登录 49，
// 补充 196 字（中间件 47 + 其余 142）。这个量级的简报不能出现任何截断。
func TestAllianceBriefKeepsFullFields(t *testing.T) {
	sys := &db.AllianceSystem{
		ID:        8,
		Name:      "某某银行综合积分权益平台托管系统",
		URL:       "https://ttt.ebanktest.com.cn:18248/html/10124/view/login/login.html",
		Platform:  "Web",
		Framework: "SSH（Struts2+Spring+Hibernate）；商城 H5 用 uniapp；微服务 Dubbo+Zookeeper；自有产品 + 行内定制",
		Crypto:    "账号+口令登录（商城端、商户端两套入口）；是否前端加密/验证码/token 机制未说明，测试时确认",
		Business:  "面向行内及外行客户的权益/会员/积分商城平台：积分体系、权益商城、活动与拼团裂变、会员成长、网点积分工具。",
		SecurityNote: "中间件/组件：Nginx、Redis、RocketMQ、Dubbo+Zookeeper、达梦数据库（DM）\n" +
			"测试账号 13800000000 / 13900000000 / 13700000000，口令均 test1234\n" +
			"达梦数据库（不是 MySQL，注入/报错特征不同）；自有产品，除权益商城外多为行内定制\n" +
			"测试环境，严禁改动或删除生产类数据\n" +
			"四期建设内容较长，原文见对接人提供的系统说明",
	}
	brief := buildAllianceBrief([]*db.AllianceSystem{sys}, nil)
	t.Logf("简报（%d 字）：\n%s", len([]rune(brief)), brief)

	if strings.Contains(brief, "…") {
		t.Errorf("这个长度的台账不该出现任何截断: %s", brief)
	}
	for _, want := range []string{
		"自有产品 + 行内定制",  // 框架末尾
		"会员成长、网点积分工具。", // 业务末尾
		"测试时确认",        // 登录末尾
		"中间件：Nginx、Redis、RocketMQ、Dubbo+Zookeeper、达梦数据库（DM）",
		"13800000000 / 13900000000 / 13700000000，口令均 test1234",
		"原文见对接人提供的系统说明", // 补充最后一行
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("简报里缺少 %q", want)
		}
	}
	if n := len([]rune(brief)); n > allianceBriefMaxRunes {
		t.Errorf("简报长度 %d 超出上限 %d", n, allianceBriefMaxRunes)
	}
}

// 超长字段仍要截断，但不能从账号中间切。
func TestAllianceBriefClipsLongFieldsAtBoundary(t *testing.T) {
	sys := &db.AllianceSystem{
		ID:           9,
		Name:         "超长字段系统",
		Framework:    strings.Repeat("框架描述；", 40),
		Business:     strings.Repeat("业务描述；", 40),
		Crypto:       strings.Repeat("登录描述；", 40),
		SecurityNote: "中间件/组件：" + strings.Repeat("组件；", 40) + "\n测试账号 13800000000，口令 test1234",
	}
	brief := buildAllianceBrief([]*db.AllianceSystem{sys}, nil)
	t.Logf("超长字段简报：%s", brief)
	if !strings.Contains(brief, "13800000000") || !strings.Contains(brief, "test1234") {
		t.Errorf("补充里的账号不该被切断: %s", brief)
	}
	if n := len([]rune(brief)); n > allianceBriefMaxRunes {
		t.Errorf("简报长度 %d 超出上限 %d", n, allianceBriefMaxRunes)
	}
}

// 多系统时总长仍受上限约束，超出的系统只报数量。
func TestAllianceBriefBudgetAndOverflowNote(t *testing.T) {
	systems := make([]*db.AllianceSystem, 0, 15)
	for i := 1; i <= 15; i++ {
		systems = append(systems, &db.AllianceSystem{
			ID:           int64(i),
			Name:         fmt.Sprintf("系统%d", i),
			URL:          fmt.Sprintf("https://s%d.example.com", i),
			Platform:     "Web",
			Framework:    strings.Repeat("框架描述", 30),
			Business:     strings.Repeat("业务描述", 30),
			Crypto:       strings.Repeat("登录描述", 30),
			SecurityNote: "中间件/组件：" + strings.Repeat("组件", 40) + "\n" + strings.Repeat("补充", 60),
		})
	}
	brief := buildAllianceBrief(systems, nil)
	if n := len([]rune(brief)); n > allianceBriefMaxRunes {
		t.Fatalf("简报长度 %d 超出上限 %d", n, allianceBriefMaxRunes)
	}
	if !strings.Contains(brief, "个选中系统未展开") {
		t.Errorf("超出预算时应说明还有多少系统未展开：%s", brief)
	}
}

// 补充为空时不应出现「补充：」空段；补充里没有中间件行时也不该冒出「中间件：」。
func TestAllianceBriefOmitsEmptySegments(t *testing.T) {
	sys := &db.AllianceSystem{ID: 1, Name: "只有一个名字的系统"}
	brief := buildAllianceBrief([]*db.AllianceSystem{sys}, nil)
	if strings.Contains(brief, "补充：") || strings.Contains(brief, "中间件：") {
		t.Errorf("空字段不该出现：%s", brief)
	}

	sys2 := &db.AllianceSystem{
		ID: 2, Name: "带补充的系统",
		SecurityNote: "该系统的中间件没填，只写了账号：admin/123456",
	}
	brief2 := buildAllianceBrief([]*db.AllianceSystem{sys2}, nil)
	if strings.Contains(brief2, "中间件：") {
		t.Errorf("没有中间件行时不该输出中间件段：%s", brief2)
	}
	if !strings.Contains(brief2, "admin/123456") {
		t.Errorf("补充内容应保留：%s", brief2)
	}
}
