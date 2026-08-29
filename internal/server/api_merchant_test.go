package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// TestMerchantMailNewFormatTemp 验证新邮件排版:
//  1. 全天商品归「全天售卖」组,不打具体时间点
//  2. 标准时段商品按组分,其他归「其他时段」
//  3. prop → 道具 翻译
//  4. 商人名不再出现「远行商人 远行商人」重复
//  5. 组装后全消息每一行 ≤ 998 字节(RFC 5321)
//  6. CID 引用与附件 base64 往返一致
func TestMerchantMailNewFormatTemp(t *testing.T) {
	items := []merchantItem{
		{Name: "全天精灵蛋", Kind: "egg", Price: 100, TimeLabel: "08:00-12:00 / 12:00-16:00 / 16:00-20:00 / 20:00-24:00", Image: "HeadIcon/3001.webp"},
		{Name: "通用道具", Kind: "prop", Price: 999999, Limit: 99, TimeLabel: "08:00-12:00 / 12:00-16:00"},
		{Name: "下午茶", Kind: "food", Price: 50, TimeLabel: "16:00-20:00"},
		{Name: "限时夜宵", Kind: "", Price: 10, TimeLabel: "21:00-23:00"},
		{Name: "无时段商品", Kind: "unknownKind", Price: 1},
	}
	imgs := []merchantMailImg{}
	content := merchantMailContent("远行商人「云上仙岛」", "2026-08-29", "08:00 ~ 12:00", items, &imgs)

	// 商人名不重复
	if strings.Contains(content, "远行商人「远行商人") {
		t.Fatalf("商人名重复: %s", content)
	}
	if !strings.Contains(content, "远行商人「云上仙岛」") {
		t.Fatalf("缺少商人名标题")
	}
	// 分组
	if strings.Count(content, "全天售卖") != 1 {
		t.Fatalf("应有 1 个全天售卖组")
	}
	if !strings.Contains(content, "全天精灵蛋") {
		t.Fatalf("全天商品应进全天组")
	}
	for _, title := range []string{"08:00-12:00", "16:00-20:00", "其他时段"} {
		if !strings.Contains(content, title) {
			t.Fatalf("缺少分组标题 %s", title)
		}
	}
	if strings.Count(content, "08:00-12:00") != 1 {
		t.Fatalf("08:00-12:00 只应出现在组标题(全天商品不该打时间): %s", content)
	}
	// kind 翻译
	if !strings.Contains(content, "道具") {
		t.Fatalf("prop 应翻译为道具")
	}
	if !strings.Contains(content, "精灵蛋") {
		t.Fatalf("egg 应翻译为精灵蛋")
	}
	if !strings.Contains(content, "unknownKind") {
		t.Fatalf("未知 kind 应原样保留")
	}
	// 图片收集
	if len(imgs) != 1 {
		t.Fatalf("期望收集 1 张图,实际 %d", len(imgs))
	}
	big := make([]byte, 100<<10)
	rand.Read(big)
	imgs[0].data = big

	// 组装完整消息,验证每行 ≤998
	html := strings.Replace(merchantMailHTMLTpl, "%s", content, 1)
	msg := merchantMailMessage("from@x.com", "to@x.com", "Sub", html, imgs)
	max := 0
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if len(line) > max {
			max = len(line)
		}
		if len(line) > 998 {
			disp := line
			if len(disp) > 120 {
				disp = disp[:120]
			}
			t.Fatalf("存在超长行 %d 字节: %q", len(line), disp)
		}
	}
	t.Logf("新格式最大单行 %d 字节(限制 998),通过", max)

	// CID 与附件 base64 往返
	if !strings.Contains(content, "cid:merchant1") {
		t.Fatalf("商品图应为 cid 引用")
	}
	s := msg[strings.Index(msg, "Content-ID: <merchant1>"):]
	enc := s[strings.Index(s, "\r\n\r\n")+4:]
	enc = enc[:strings.Index(enc, "\r\n--")]
	enc = strings.ReplaceAll(enc, "\r\n", "")
	got, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("附件 base64 往返不一致: %v", err)
	}
	t.Logf("附件 base64 往返一致,通过")
	t.Logf("--- 内容区 HTML 预览 ---\n%s", content)
}
