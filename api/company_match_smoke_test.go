package main

import "testing"

// 真实生产检索数据烟测（2026-08-17 实测快照）
func TestMatchFoodTalksCompanyRealData(t *testing.T) {
	hema := []foodTalksCompany{
		{ID: 5456, CnName: "盒马（中国）有限公司", ShortCnName: "盒马,盒马鲜生", URLCode: "Freshippo"},
		{ID: 484, CnName: "菓子熟了（南京）食品有限公司", ShortCnName: "果子熟了", URLCode: "fruit_ripening_biotechnology"},
	}
	if got := matchFoodTalksCompany(hema, "盒马"); got == nil || got.ID != 5456 {
		t.Fatalf("盒马应命中 5456：%+v", got)
	}
	sams := []foodTalksCompany{
		{ID: 15259, CnName: "山姆会员商店中国", ShortCnName: "山姆中国,山姆,山姆会员店", URLCode: "sams_club_china"},
		{ID: 1229, CnName: "沃尔玛（中国）投资有限公司", ShortCnName: "沃尔玛中国,沃尔玛超市", URLCode: "Walmart_China"},
	}
	if got := matchFoodTalksCompany(sams, "山姆"); got == nil || got.ID != 15259 {
		t.Fatalf("山姆应命中中国主体 15259：%+v", got)
	}
	kombucha := []foodTalksCompany{{ID: 5087, CnName: "新乡市天界山生物科技有限公司", ShortCnName: "", URLCode: "x"}}
	if got := matchFoodTalksCompany(kombucha, "康普茶"); got != nil {
		t.Fatalf("品类词康普茶不得命中：%+v", got)
	}
	heytea := []foodTalksCompany{
		{ID: 521, CnName: "深圳美西西餐饮管理有限公司", ShortCnName: "喜茶", URLCode: "Hey_Tea"},
		{ID: 5765, CnName: "红杉中国", ShortCnName: "", URLCode: "Sequoia"},
	}
	if got := matchFoodTalksCompany(heytea, "喜茶"); got == nil || got.ID != 521 {
		t.Fatalf("喜茶应命中美西西 521：%+v", got)
	}
}
