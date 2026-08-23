package main

// feishu_notify_test.go — 飞书通知测试（httptest 桩，不触真实飞书）。
// 通知是流水线的加速器不是闸门，但 token 缓存和禁用语义仍要钉住：
// 缓存失效会让每条通知多打一次鉴权接口，env 缺失必须静默禁用而非报错刷屏。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCoverPNG(t *testing.T, width, height int, fill color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test cover: %v", err)
	}
	return buf.Bytes()
}

func colorNear(got, want color.RGBA, tol int) bool {
	dr, dg, db := int(got.R)-int(want.R), int(got.G)-int(want.G), int(got.B)-int(want.B)
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	return dr <= tol && dg <= tol && db <= tol
}

// TestSquareFeishuCover 封面上传前先生成 1:1 画布：原图完整居中，不通过
// crop_center 截掉商品或图内文字；空余区域使用设计系统的嵌套灰。
func TestSquareFeishuCover(t *testing.T) {
	red := color.RGBA{R: 220, G: 40, B: 40, A: 255}
	for _, tc := range []struct {
		name          string
		width, height int
		paddingPoint  image.Point
	}{
		{name: "portrait", width: 2, height: 4, paddingPoint: image.Pt(0, feishuCoverSquareSize/2)},
		{name: "landscape", width: 4, height: 2, paddingPoint: image.Pt(feishuCoverSquareSize/2, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := squareFeishuCover(testCoverPNG(t, tc.width, tc.height, red))
			if err != nil {
				t.Fatalf("square cover: %v", err)
			}
			img, format, err := image.Decode(bytes.NewReader(got))
			if err != nil || format != "jpeg" {
				t.Fatalf("decode squared cover: format=%q err=%v", format, err)
			}
			if bounds := img.Bounds(); bounds.Dx() != feishuCoverSquareSize || bounds.Dy() != feishuCoverSquareSize {
				t.Fatalf("封面必须为 %dx%d，实际 %v", feishuCoverSquareSize, feishuCoverSquareSize, bounds)
			}
			pad := color.RGBAModel.Convert(img.At(tc.paddingPoint.X, tc.paddingPoint.Y)).(color.RGBA)
			if !colorNear(pad, feishuCoverBackground, 8) {
				t.Fatalf("补边颜色应为 %#v，实际 %#v", feishuCoverBackground, pad)
			}
			center := color.RGBAModel.Convert(img.At(feishuCoverSquareSize/2, feishuCoverSquareSize/2)).(color.RGBA)
			if center.R < 200 || center.G > 60 || center.B > 60 {
				t.Fatalf("原图主体应完整居中保留，中心像素实际 %#v", center)
			}
		})
	}
}

// TestSquareFeishuCoverStaysSmall 照片级封面必须压到卡片缩略图能秒开的体积。
func TestSquareFeishuCoverStaysSmall(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1200, 675))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < 675; y++ {
		for x := 0; x < 1200; x++ {
			src.Set(x, y, color.RGBA{
				R: uint8(80 + rng.Intn(140)),
				G: uint8(60 + rng.Intn(120)),
				B: uint8(40 + rng.Intn(100)),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode source: %v", err)
	}
	got, err := squareFeishuCover(buf.Bytes())
	if err != nil {
		t.Fatalf("square cover: %v", err)
	}
	t.Logf("squared jpeg: %d bytes", len(got))
	if len(got) > 120_000 {
		t.Fatalf("封面应远小于 120KB，实际 %d KB", len(got)/1024)
	}
}

// TestFeishuNotifierSendAndTokenCache 发文本走 tenant_access_token 两步：token 只取一次
// （缓存生效），消息体形态与 receive_id_type=chat_id 契约对齐。
func TestFeishuNotifierSendAndTokenCache(t *testing.T) {
	tokenCalls := 0
	var gotAuth, gotQuery string
	var gotMsg struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls++
			var req struct {
				AppID     string `json:"app_id"`
				AppSecret string `json:"app_secret"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.AppID != "cli_test" || req.AppSecret != "secret_test" {
				t.Errorf("鉴权凭证不符：%+v", req)
			}
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-1","expire":7200}`)
		case "/open-apis/im/v1/messages":
			gotAuth = r.Header.Get("Authorization")
			gotQuery = r.URL.RawQuery
			json.NewDecoder(r.Body).Decode(&gotMsg)
			fmt.Fprint(w, `{"code":0,"msg":"ok"}`)
		default:
			t.Errorf("飞书桩收到意外路径 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n := &feishuNotifier{
		baseURL:     srv.URL,
		appID:       "cli_test",
		appSecret:   "secret_test",
		receiveID:   "oc_test_chat",
		receiveType: feishuReceiveIDTypeChat,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
	if err := n.send(context.Background(), "第一条"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if err := n.send(context.Background(), "第二条"); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token 应缓存复用（1 次），实际取了 %d 次", tokenCalls)
	}
	if gotAuth != "Bearer tok-1" || gotQuery != "receive_id_type=chat_id" {
		t.Fatalf("发消息请求形态不符：auth=%q query=%q", gotAuth, gotQuery)
	}
	if gotMsg.ReceiveID != "oc_test_chat" || gotMsg.MsgType != "text" || !strings.Contains(gotMsg.Content, "第二条") {
		t.Fatalf("消息体不符：%+v", gotMsg)
	}
}

func TestFeishuNotifierCardLifecycleUsesMessageID(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotUpdate struct {
		Content string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-card","expire":7200}`)
		case "/open-apis/im/v1/messages":
			gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"message_id":"om_card_1"}}`)
		case "/open-apis/im/v1/messages/om_card_1":
			gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&gotUpdate); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			fmt.Fprint(w, `{"code":0,"msg":"ok"}`)
		default:
			t.Errorf("飞书桩收到意外路径 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n := &feishuNotifier{
		baseURL: srv.URL, appID: "cli_test", appSecret: "secret_test",
		receiveID: "oc_test_chat", receiveType: feishuReceiveIDTypeChat, httpClient: srv.Client(),
	}
	card := map[string]any{"schema": "2.0", "header": map[string]any{"title": "待审核"}}
	messageID, err := n.sendCardWithID(context.Background(), card)
	if err != nil || messageID != "om_card_1" {
		t.Fatalf("发送审核卡应返回 message_id：id=%q err=%v", messageID, err)
	}
	if err := n.updateCard(context.Background(), messageID, map[string]any{"schema": "2.0", "header": map[string]any{"title": "已发布"}}); err != nil {
		t.Fatalf("原位更新审核卡: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/open-apis/im/v1/messages/om_card_1" || gotAuth != "Bearer tok-card" {
		t.Fatalf("更新卡片请求形态不符：method=%q path=%q auth=%q", gotMethod, gotPath, gotAuth)
	}
	var updatedCard map[string]any
	if err := json.Unmarshal([]byte(gotUpdate.Content), &updatedCard); err != nil {
		t.Fatalf("更新 content 应是卡片 JSON: %v", err)
	}
	if updatedCard["schema"] != "2.0" {
		t.Fatalf("更新 content 不是卡片本体：%v", updatedCard)
	}
}

func TestFeishuMonitorNotifierUsesPrivateUserID(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret_test")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_review_group")
	t.Setenv("PUBLISH_MONITOR_FEISHU_USER_ID", "test-monitor-user")
	var gotQuery string
	var gotMessage struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-private","expire":7200}`)
		case "/open-apis/im/v1/messages":
			gotQuery = r.URL.RawQuery
			json.NewDecoder(r.Body).Decode(&gotMessage)
			fmt.Fprint(w, `{"code":0,"msg":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n := newFeishuMonitorNotifierFromEnv()
	if n == nil {
		t.Fatal("监控私聊配置完整时 notifier 不得为 nil")
	}
	n.baseURL = srv.URL
	n.httpClient = srv.Client()
	if err := n.send(context.Background(), "监控私聊"); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "receive_id_type=user_id" || gotMessage.ReceiveID != "test-monitor-user" || gotMessage.MsgType != "text" {
		t.Fatalf("监控通知必须只发 user_id 私聊：query=%q message=%+v", gotQuery, gotMessage)
	}
}

// TestFeishuNotifierUploadCoverUsesSquareJPEG 守住真实上传路径：飞书收到的必须已经是
// 方形 JPEG 素材，卡片端只负责按宽度展示，不再承担裁切。
func TestFeishuNotifierUploadCoverUsesSquareJPEG(t *testing.T) {
	portrait := testCoverPNG(t, 2, 4, color.RGBA{R: 220, G: 40, B: 40, A: 255})
	var uploadedBounds image.Rectangle
	var uploadedFormat, uploadedName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-cover","expire":7200}`)
		case "/cover.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(portrait)
		case "/open-apis/im/v1/images":
			if got := r.Header.Get("Authorization"); got != "Bearer tok-cover" {
				t.Errorf("上传鉴权不符：%q", got)
			}
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("parse upload form: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if got := r.FormValue("image_type"); got != "message" {
				t.Errorf("image_type=%q", got)
			}
			file, header, err := r.FormFile("image")
			if err != nil {
				t.Errorf("read uploaded image: %v", err)
				http.Error(w, "bad image", http.StatusBadRequest)
				return
			}
			defer file.Close()
			uploadedName = header.Filename
			img, format, err := image.Decode(file)
			if err != nil {
				t.Errorf("decode uploaded image: %v", err)
				http.Error(w, "bad image", http.StatusBadRequest)
				return
			}
			uploadedBounds, uploadedFormat = img.Bounds(), format
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"image_key":"img_square"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n := &feishuNotifier{
		baseURL: srv.URL, appID: "a", appSecret: "b", httpClient: srv.Client(),
	}
	key, err := n.uploadImageByURL(context.Background(), srv.URL+"/cover.png")
	if err != nil || key != "img_square" {
		t.Fatalf("upload cover: key=%q err=%v", key, err)
	}
	if uploadedName != "cover.jpg" || uploadedFormat != "jpeg" || uploadedBounds.Dx() != feishuCoverSquareSize || uploadedBounds.Dy() != feishuCoverSquareSize {
		t.Fatalf("飞书收到的封面必须是方形 JPEG：name=%q format=%q bounds=%v", uploadedName, uploadedFormat, uploadedBounds)
	}
}

// TestFeishuNotifierDisabledWhenEnvMissing FEISHU_* 任一缺失 → 构造返回 nil（通知禁用），
// 流水线照常运行——通知配置永远不该成为发布的阻塞项。
func TestFeishuNotifierDisabledWhenEnvMissing(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_x")
	if n := newFeishuNotifierFromEnv(); n != nil {
		t.Fatalf("缺 FEISHU_APP_SECRET 应禁用通知，实际返回了 notifier")
	}
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("PUBLISH_MONITOR_FEISHU_USER_ID", "")
	if n := newFeishuMonitorNotifierFromEnv(); n != nil {
		t.Fatalf("缺监控私聊 user_id 应禁用监控通知，实际返回了 notifier")
	}
	// 业务失败（code!=0）要转成 error 让调用方 WARN，不能静默吞掉。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok","expire":7200}`)
			return
		}
		fmt.Fprint(w, `{"code":230002,"msg":"bot not in chat"}`)
	}))
	defer srv.Close()
	n := &feishuNotifier{
		baseURL: srv.URL, appID: "a", appSecret: "b", receiveID: "c", receiveType: feishuReceiveIDTypeChat, httpClient: srv.Client(),
	}
	if err := n.send(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "230002") {
		t.Fatalf("code!=0 应报错并携带 code，实际 %v", err)
	}
}
