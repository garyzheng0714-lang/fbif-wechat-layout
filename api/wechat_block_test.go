package main

import "testing"

func TestLooksLikeWeChatBlock(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"real article (long, no markers)", "本文介绍食品行业的创新趋势，内容丰富，段落众多。" + longStr(3000), false},
		{"env exception stub", "## 环境异常\n\n当前环境异常，完成验证后即可继续访问。", true},
		{"captcha warning", "Warning: This page maybe requiring CAPTCHA, please make sure you are authorized.", true},
		{"captcha word only (short)", "CAPTCHA required", true},
		{"long article that merely mentions CAPTCHA", "关于安全登录" + longStr(3000) + "CAPTCHA", false},
	}
	for _, c := range cases {
		got := looksLikeWeChatBlock(c.in)
		if got != c.want {
			t.Errorf("%s: looksLikeWeChatBlock(...) = %v, want %v", c.name, got, c.want)
		}
	}
}

func longStr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
