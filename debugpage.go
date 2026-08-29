package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/chromedp/chromedp"
)

// quoteJS は文字列をJavaScriptのリテラルとして安全に埋め込める形に変換する。
func quoteJS(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		log.Printf("セレクタのエスケープに失敗しました (%q): %v", s, err)
		return `""`
	}
	return string(b)
}

// runDebugActivityPage は活動記録ページのDOM構造を調査するデバッグ用アクション。
// YAMAPのフロントエンド刷新でリアクション関連のセレクタが合わなくなった際の調査に使う。
//
// 調査対象は targetURL で指定する。空の場合はタイムラインから1件拾う。
func runDebugActivityPage(targetURL string) {
	log.Println("--- プログラム開始 (debug-activity-page) ---")

	ctx, cancel := setupBrowser(20*time.Minute, 15*time.Minute)
	defer cancel()

	if err := loginForFollowActions(ctx); err != nil {
		log.Fatalf("%v", err)
	}

	if targetURL == "" {
		log.Println("-url が未指定のため、タイムラインから活動記録を1件取得します...")
		if err := chromedp.Run(ctx,
			chromedp.Navigate("https://yamap.com/timeline"),
			chromedp.Sleep(10*time.Second),
			chromedp.Evaluate(`
				(function(){
					var a = document.querySelector('a[href^="/activities/"]');
					return a ? "https://yamap.com" + a.getAttribute('href') : "";
				})()
			`, &targetURL),
		); err != nil {
			log.Fatalf("タイムラインからの取得に失敗しました: %v", err)
		}
		if targetURL == "" {
			log.Fatal("タイムラインから活動記録のリンクを取得できませんでした。")
		}
	}
	log.Printf("調査対象: %s", targetURL)

	if err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.Sleep(10*time.Second),
	); err != nil {
		log.Fatalf("活動記録ページへの移動に失敗しました: %v", err)
	}

	var title string
	_ = chromedp.Run(ctx, chromedp.Title(&title))
	log.Printf("title=%s", title)

	log.Println("\n=== 旧セレクタの生存確認 ===")
	for _, sel := range []string{
		".FooterNav",
		".ActivitiesId__ActivityToolBarContainer",
		".emoji-add-button",
		".emojiPickerBody",
		".emojiButton",
		".emoji-picker-button",
		// 現行のセレクタ。ピッカーを開く前の時点で存在するかを確認する
		".emoji-button",
		".viewer-has-reacted",
		`button[aria-label="絵文字をおくる"]`,
		`a[aria-label^="絵文字をおくった人"]`,
	} {
		var n int
		_ = chromedp.Run(ctx, chromedp.Evaluate(
			`document.querySelectorAll(`+quoteJS(sel)+`).length`, &n))
		log.Printf("  %-45s %d件", sel, n)
	}

	probes := []struct {
		label  string
		script string
	}{
		{"button の aria-label 一覧", `
			Array.from(new Set(Array.from(document.querySelectorAll('button[aria-label]'))
			     .map(function(b){return b.getAttribute('aria-label');}))).slice(0,40).join(" | ")`},
		{"button のテキスト一覧", `
			Array.from(new Set(Array.from(document.querySelectorAll('button'))
			     .map(function(b){return b.innerText.trim();})
			     .filter(function(t){return t && t.length < 20;}))).slice(0,40).join(" | ")`},
		{"data-testid 一覧", `
			Array.from(new Set(Array.from(document.querySelectorAll('[data-testid]'))
			     .map(function(e){return e.getAttribute('data-testid');}))).slice(0,40).join(" | ")`},
		{"絵文字/リアクション関連の要素", `
			Array.from(document.querySelectorAll('*'))
			     .filter(function(e){
			        var a = (e.getAttribute && (e.getAttribute('aria-label') || '')) || '';
			        var c = (typeof e.className === 'string' ? e.className : '');
			        return /絵文字|リアクション|emoji|reaction/i.test(a + ' ' + c);
			     })
			     .slice(0,15)
			     .map(function(e){
			        return e.tagName.toLowerCase()
			             + (e.getAttribute('aria-label') ? '[aria-label=' + e.getAttribute('aria-label') + ']' : '')
			             + (typeof e.className === 'string' && e.className ? '.' + e.className.trim().split(/\s+/).join('.') : '');
			     }).join("  //  ")`},
		{"ページ内のクラス名（先頭60）", `
			Array.from(new Set(Array.from(document.querySelectorAll('[class]'))
			     .flatMap(function(e){return Array.from(e.classList);}))).slice(0,60).join(", ")`},
	}

	for _, p := range probes {
		var out string
		if err := chromedp.Run(ctx, chromedp.Evaluate(p.script, &out)); err != nil {
			log.Printf("%s: 取得失敗 (%v)", p.label, err)
			continue
		}
		log.Printf("\n[%s]\n%s", p.label, out)
	}

	// リアクションボタンを押してピッカーの構造を調べる。
	// 絵文字自体はクリックしないため、この時点では投稿にリアクションは付かない。
	log.Println("\n=== 絵文字ピッカーの構造 ===")
	const reactionButton = `button[aria-label="絵文字をおくる"]`

	var btnCount int
	_ = chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll(`+quoteJS(reactionButton)+`).length`, &btnCount))
	log.Printf("%s: %d件", reactionButton, btnCount)
	if btnCount == 0 {
		log.Println("リアクションボタンが見つからないため、ピッカーの調査を中止します。")
		log.Println("\n--- 調査完了 ---")
		return
	}

	if err := chromedp.Run(ctx,
		chromedp.ScrollIntoView(reactionButton, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Click(reactionButton, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		log.Printf("リアクションボタンのクリックに失敗しました: %v", err)
		log.Println("\n--- 調査完了 ---")
		return
	}
	log.Println("リアクションボタンをクリックしました。ピッカーの中身を調べます。")

	pickerProbes := []struct {
		label  string
		script string
	}{
		{"role=dialog / listbox の有無", `
			Array.from(document.querySelectorAll('[role]'))
			     .map(function(e){return e.getAttribute('role');})
			     .filter(function(r){return /dialog|listbox|menu|tooltip/i.test(r);})
			     .join(", ") || "(該当なし)"`},
		{"新たに出現したボタンの aria-label", `
			Array.from(new Set(Array.from(document.querySelectorAll('button[aria-label]'))
			     .map(function(b){return b.getAttribute('aria-label');}))).slice(0,60).join(" | ")`},
		{"絵文字らしきボタン（テキストが記号1〜4文字）", `
			(function(){
			  var bs = Array.from(document.querySelectorAll('button'))
			      .filter(function(b){
			         var t = b.innerText.trim();
			         return t.length > 0 && t.length <= 4 && !/^[a-zA-Z0-9]+$/.test(t);
			      });
			  return "件数=" + bs.length + " / 先頭10=" + bs.slice(0,10).map(function(b){
			     return b.innerText.trim()
			          + "[aria-label=" + (b.getAttribute('aria-label')||'') + "]";
			  }).join(" ");
			})()`},
		{"絵文字ボタンの属性（リアクション済み判定の手がかり）", `
			(function(){
			  var dlg = document.querySelector('[role="dialog"]');
			  if (!dlg) return "(dialog が見つからない)";
			  var bs = Array.from(dlg.querySelectorAll('button[aria-label]')).slice(0,5);
			  return bs.map(function(b){
			     var attrs = Array.from(b.attributes)
			         .map(function(a){return a.name + "=" + a.value;})
			         .join(" ");
			     return "<" + attrs.slice(0,300) + ">";
			  }).join("\n");
			})()`},
		{"ツールバー側の既存リアクション表示", `
			(function(){
			  var links = Array.from(document.querySelectorAll('a[aria-label*="絵文字"]'));
			  if (!links.length) return "(該当なし)";
			  return links.map(function(a){
			     return a.getAttribute('aria-label') + " => text=" + a.innerText.trim().slice(0,40);
			  }).join(" // ");
			})()`},
		{"img/span による絵文字表現", `
			(function(){
			  var imgs = Array.from(document.querySelectorAll('img[alt]'))
			      .filter(function(i){ return i.alt && i.alt.length <= 12; });
			  return "img[alt]件数=" + imgs.length + " / 先頭10=" +
			     imgs.slice(0,10).map(function(i){return i.alt;}).join(", ");
			})()`},
	}

	for _, p := range pickerProbes {
		var out string
		if err := chromedp.Run(ctx, chromedp.Evaluate(p.script, &out)); err != nil {
			log.Printf("%s: 取得失敗 (%v)", p.label, err)
			continue
		}
		log.Printf("\n[%s]\n%s", p.label, out)
	}

	log.Println("\n--- 調査完了 ---")
}
