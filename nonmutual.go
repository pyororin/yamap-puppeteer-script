package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// nonMutualListFile は片思いフォロー（こちらがフォローしているが相手はフォローしていない）
// のユーザー一覧を保存するファイル名。
const nonMutualListFile = "non_mutual_unique.txt"

// FollowUser はフォロー中／フォロワー一覧ページの1カードから抽出した情報。
type FollowUser struct {
	Name string `json:"name"`
	Href string `json:"href"`
	// FollowsMe は相手がこちらをフォローしている（カードに「フォローされています」がある）か。
	FollowsMe bool `json:"followsMe"`
	// ButtonLabels はカード内のボタンのラベル一覧。フォロー状態の判定に使う。
	ButtonLabels []string `json:"buttonLabels"`
}

// URL はユーザーページの絶対URLを返す。
func (u FollowUser) URL() string {
	return "https://yamap.com" + u.Href
}

// setupBrowser はこのファイル内のアクションが共通で使うブラウザコンテキストを構築する。
// 返り値の cancel を呼ぶと確保したリソースがすべて解放される。
func setupBrowser(total, operation time.Duration) (context.Context, context.CancelFunc) {
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), total)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Headless,
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		// CI環境ではChromeの起動が既定の20秒を超えて
		// "websocket url timeout reached" で失敗することがあるため延長する
		chromedp.WSURLReadTimeout(90*time.Second),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(allocatorCtx, allocOpts...)

	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	ctx, cancelTimeout := context.WithTimeout(ctx, operation)

	cancel := func() {
		cancelTimeout()
		cancelCtx()
		cancelAlloc()
		cancelAllocator()
	}
	return ctx, cancel
}

// loginForFollowActions は環境変数を読み、ログインまで済ませる。
func loginForFollowActions(ctx context.Context) error {
	email := os.Getenv("YAMAP_EMAIL")
	password := os.Getenv("YAMAP_PASSWORD")
	if email == "" || password == "" {
		return fmt.Errorf("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD を設定してください")
	}

	log.Println("ログイン処理を開始します...")
	start := time.Now()
	if err := login(ctx, email, password, false); err != nil {
		return fmt.Errorf("ログインに失敗しました: %w", err)
	}
	log.Printf("ログイン成功。処理時間: %s", time.Since(start))
	return nil
}

// scrapeFollowCards は現在開いているフォロー一覧ページからユーザーカードを抽出する。
// フォローバック処理と同じく article 要素を1カードとして扱う。
func scrapeFollowCards(ctx context.Context) ([]FollowUser, error) {
	var raw string
	script := `
		(function() {
			var cards = document.querySelectorAll('article');
			var out = [];
			for (var i = 0; i < cards.length; i++) {
				var card = cards[i];
				var link = card.querySelector('a[href^="/users/"]');
				if (!link) continue;

				var labels = [];
				var buttons = card.querySelectorAll('button');
				for (var j = 0; j < buttons.length; j++) {
					var t = buttons[j].innerText.trim();
					if (t) labels.push(t);
				}

				out.push({
					name: (link.innerText || '').trim(),
					href: link.getAttribute('href'),
					followsMe: card.innerText.includes('フォローされています'),
					buttonLabels: labels
				});
			}
			return JSON.stringify(out);
		})()
	`
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &raw)); err != nil {
		return nil, fmt.Errorf("カードの抽出に失敗しました: %w", err)
	}

	var users []FollowUser
	if err := json.Unmarshal([]byte(raw), &users); err != nil {
		return nil, fmt.Errorf("カード情報のパースに失敗しました: %w", err)
	}
	return users, nil
}

// gotoNextFollowPage は次ページがあれば移動して true を返す。
func gotoNextFollowPage(ctx context.Context) (bool, error) {
	var hasNext bool
	err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var b = document.querySelector('button[aria-label="次のページに移動する"]');
			return b !== null && !b.disabled;
		})()
	`, &hasNext))
	if err != nil {
		return false, fmt.Errorf("次ページボタンの確認に失敗しました: %w", err)
	}
	if !hasNext {
		return false, nil
	}

	if err := chromedp.Run(ctx,
		chromedp.Click(`button[aria-label="次のページに移動する"]`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second),
	); err != nil {
		return false, fmt.Errorf("次ページへの移動に失敗しました: %w", err)
	}
	return true, nil
}

// collectFollowingUsers はフォロー中一覧を全ページ巡回して集める。
func collectFollowingUsers(ctx context.Context, userID string) ([]FollowUser, error) {
	pageURL := fmt.Sprintf("https://yamap.com/users/%s?tab=follows#tabs", userID)
	log.Printf("フォロー中一覧に移動します: %s", pageURL)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(10*time.Second),
	); err != nil {
		return nil, fmt.Errorf("フォロー中一覧への移動に失敗しました: %w", err)
	}

	seen := make(map[string]struct{})
	var all []FollowUser

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			log.Println("コンテキストがキャンセルされたため、収集を中断します。")
			break
		}

		users, err := scrapeFollowCards(ctx)
		if err != nil {
			return all, err
		}

		added := 0
		for _, u := range users {
			if u.Href == "" {
				continue
			}
			if _, dup := seen[u.Href]; dup {
				continue
			}
			seen[u.Href] = struct{}{}
			all = append(all, u)
			added++
		}
		log.Printf("--- %dページ目: %d件を検出（新規 %d件 / 累計 %d件）---", page, len(users), added, len(all))

		if len(users) == 0 {
			log.Println("カードが1件も取得できませんでした。ページ構造の変更を疑ってください。")
			break
		}

		next, err := gotoNextFollowPage(ctx)
		if err != nil {
			log.Printf("%v", err)
			break
		}
		if !next {
			log.Println("最後のページに到達しました。")
			break
		}
	}

	return all, nil
}

// IFollow は自分がこのユーザーをフォロー中かを返す。
// フォロー中一覧のページには「おすすめのユーザー」も同じ article として混在しており、
// そちらは「フォローする」ボタンを持つ。「フォロー中」ボタンの有無で確実に切り分ける。
func (u FollowUser) IFollow() bool {
	for _, l := range u.ButtonLabels {
		if l == "フォロー中" {
			return true
		}
	}
	return false
}

// filterNonMutual は「こちらがフォローしているが相手はフォローしていない」ユーザーを抽出する。
func filterNonMutual(users []FollowUser) []FollowUser {
	var out []FollowUser
	for _, u := range users {
		if u.IFollow() && !u.FollowsMe {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Href < out[j].Href })
	return out
}

// writeNonMutualList は片思いフォローの一覧をファイルへ書き出す。
func writeNonMutualList(users []FollowUser) error {
	f, err := os.Create(nonMutualListFile)
	if err != nil {
		return fmt.Errorf("%s の作成に失敗しました: %w", nonMutualListFile, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, u := range users {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", u.URL(), u.Name); err != nil {
			return fmt.Errorf("%s への書き込みに失敗しました: %w", nonMutualListFile, err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("%s のフラッシュに失敗しました: %w", nonMutualListFile, err)
	}
	return nil
}

// readNonMutualList は片思いフォロー一覧をファイルから読み込む。
func readNonMutualList() ([]FollowUser, error) {
	f, err := os.Open(nonMutualListFile)
	if err != nil {
		return nil, fmt.Errorf("%s の読み込みに失敗しました（先に -action list-non-mutual を実行してください）: %w", nonMutualListFile, err)
	}
	defer f.Close()

	var users []FollowUser
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		url := parts[0]
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		href := strings.TrimPrefix(url, "https://yamap.com")
		users = append(users, FollowUser{Name: name, Href: href})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s の走査中にエラーが発生しました: %w", nonMutualListFile, err)
	}
	return users, nil
}

// runListNonMutual は片思いフォローを洗い出してファイルに保存する。読み取りのみで副作用はない。
func runListNonMutual() {
	log.Println("--- プログラム開始 (list-non-mutual) ---")
	startTime := time.Now()

	ctx, cancel := setupBrowser(60*time.Minute, 55*time.Minute)
	defer cancel()

	if err := loginForFollowActions(ctx); err != nil {
		log.Fatalf("%v", err)
	}

	userID, err := getMyUserID(ctx)
	if err != nil {
		log.Fatalf("ユーザーIDの取得に失敗しました: %v", err)
	}
	log.Printf("ログイン中のユーザーID: %s", userID)

	all, err := collectFollowingUsers(ctx, userID)
	if err != nil {
		log.Printf("収集中にエラーが発生しました: %v", err)
	}

	following := 0
	for _, u := range all {
		if u.IFollow() {
			following++
		}
	}
	nonMutual := filterNonMutual(all)
	log.Printf("収集カード: %d件 / うちフォロー中: %d件 / うち片思い: %d件（残りはおすすめ枠）",
		len(all), following, len(nonMutual))

	if err := writeNonMutualList(nonMutual); err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("%s に書き出しました。", nonMutualListFile)

	log.Println("\n--- 片思いフォローの一覧 ---")
	for _, u := range nonMutual {
		log.Printf("%s\t%s", u.URL(), u.Name)
	}
	log.Println("----------------------------")

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("総処理時間: %s", time.Since(startTime))
}

// unfollowUser は個別のユーザーページを開いてフォローを解除する。
func unfollowUser(ctx context.Context, u FollowUser) (bool, error) {
	if err := chromedp.Run(ctx,
		chromedp.Navigate(u.URL()),
		chromedp.Sleep(5*time.Second),
	); err != nil {
		return false, fmt.Errorf("ユーザーページへの移動に失敗しました: %w", err)
	}

	// 「フォロー中」ボタンを押すとフォロー解除される。
	// 確認ダイアログが出る場合に備え、ダイアログ内の「フォロー解除」も探す。
	var result string
	script := `
		(function() {
			function findButton(labels) {
				var buttons = document.querySelectorAll('button');
				for (var i = 0; i < buttons.length; i++) {
					var t = buttons[i].innerText.trim();
					for (var j = 0; j < labels.length; j++) {
						if (t === labels[j]) return buttons[i];
					}
				}
				return null;
			}

			if (findButton(['フォローする'])) return 'not_following';

			var btn = findButton(['フォロー中', 'フォローを解除', 'フォロー解除']);
			if (!btn) return 'button_not_found';
			btn.click();
			return 'clicked';
		})()
	`
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &result)); err != nil {
		return false, fmt.Errorf("フォロー解除ボタンの操作に失敗しました: %w", err)
	}

	switch result {
	case "not_following":
		log.Printf("既にフォローしていません: %s", u.Name)
		return false, nil
	case "button_not_found":
		log.Printf("フォロー解除ボタンが見つかりませんでした: %s", u.Name)
		return false, nil
	}

	// 確認ダイアログが出た場合に確定ボタンを押す
	var confirmed string
	confirmScript := `
		(function() {
			var buttons = document.querySelectorAll('button');
			for (var i = 0; i < buttons.length; i++) {
				var t = buttons[i].innerText.trim();
				if (t === 'フォロー解除' || t === '解除する' || t === 'はい') {
					buttons[i].click();
					return 'confirmed';
				}
			}
			return 'no_dialog';
		})()
	`
	if err := chromedp.Run(ctx,
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(confirmScript, &confirmed),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return false, fmt.Errorf("確認ダイアログの処理に失敗しました: %w", err)
	}

	return true, nil
}

// runUnfollowNonMutual は片思いフォローを解除する。
// non_mutual_unique.txt を入力とするため、事前に list-non-mutual の実行が必要。
func runUnfollowNonMutual() {
	log.Println("--- プログラム開始 (unfollow-non-mutual) ---")
	startTime := time.Now()

	targets, err := readNonMutualList()
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(targets) == 0 {
		log.Printf("%s が空です。解除対象はありません。", nonMutualListFile)
		return
	}
	log.Printf("解除対象: %d件", len(targets))

	ctx, cancel := setupBrowser(90*time.Minute, 85*time.Minute)
	defer cancel()

	if err := loginForFollowActions(ctx); err != nil {
		log.Fatalf("%v", err)
	}

	var unfollowed []string
	for i, u := range targets {
		if ctx.Err() != nil {
			log.Println("コンテキストがキャンセルされたため、処理を中断します。")
			break
		}

		log.Printf("--- %d/%d を処理中: %s ---", i+1, len(targets), u.URL())
		ok, err := unfollowUser(ctx, u)
		if err != nil {
			log.Printf("解除処理でエラーが発生しました (%s): %v", u.URL(), err)
			continue
		}
		if ok {
			unfollowed = append(unfollowed, fmt.Sprintf("%s\t%s", u.URL(), u.Name))
			log.Printf("フォロー解除成功: %s (現在 %d 件)", u.Name, len(unfollowed))
		}

		// 連続リクエストを避けるための待機
		time.Sleep(2 * time.Second)
	}

	log.Println("\n--- フォロー解除したユーザー一覧 ---")
	for _, s := range unfollowed {
		log.Println(s)
	}
	log.Println("------------------------------------")

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("合計 %d 件のフォローを解除しました。", len(unfollowed))
	log.Printf("総処理時間: %s", time.Since(startTime))
}

// runDebugFollowButtons はフォロー系ページのDOM構造を調査するためのデバッグ用アクション。
// YAMAP側の仕様変更でセレクタが合わなくなった際の調査に使う。
func runDebugFollowButtons() {
	log.Println("--- プログラム開始 (debug-follow-buttons) ---")

	ctx, cancel := setupBrowser(20*time.Minute, 15*time.Minute)
	defer cancel()

	if err := loginForFollowActions(ctx); err != nil {
		log.Fatalf("%v", err)
	}

	userID, err := getMyUserID(ctx)
	if err != nil {
		log.Fatalf("ユーザーIDの取得に失敗しました: %v", err)
	}
	log.Printf("ログイン中のユーザーID: %s", userID)

	// タブ名はYAMAP側の変更を受けやすいので、候補を順に試して当たりを報告する
	for _, tab := range []string{"follows", "followings", "following", "followers"} {
		pageURL := fmt.Sprintf("https://yamap.com/users/%s?tab=%s#tabs", userID, tab)
		log.Printf("\n=== tab=%s を調査: %s ===", tab, pageURL)

		if err := chromedp.Run(ctx,
			chromedp.Navigate(pageURL),
			chromedp.Sleep(8*time.Second),
		); err != nil {
			log.Printf("移動に失敗しました: %v", err)
			continue
		}

		var articleCount, userLinkCount int
		var title string
		_ = chromedp.Run(ctx,
			chromedp.Title(&title),
			chromedp.Evaluate(`document.querySelectorAll('article').length`, &articleCount),
			chromedp.Evaluate(`document.querySelectorAll('a[href^="/users/"]').length`, &userLinkCount),
		)
		log.Printf("title=%s / article=%d件 / a[href^=\"/users/\"]=%d件", title, articleCount, userLinkCount)

		users, err := scrapeFollowCards(ctx)
		if err != nil {
			log.Printf("カード抽出に失敗しました: %v", err)
			continue
		}
		log.Printf("抽出できたカード: %d件", len(users))

		limit := 5
		if len(users) < limit {
			limit = len(users)
		}
		for i := 0; i < limit; i++ {
			u := users[i]
			log.Printf("  [%d] name=%q href=%s followsMe=%v buttons=%v",
				i, u.Name, u.Href, u.FollowsMe, u.ButtonLabels)
		}

		var hasNext bool
		_ = chromedp.Run(ctx, chromedp.Evaluate(`
			(function(){
				var b = document.querySelector('button[aria-label="次のページに移動する"]');
				return b !== null && !b.disabled;
			})()
		`, &hasNext))
		log.Printf("次ページボタン: %v", hasNext)
	}

	log.Println("--- 調査完了 ---")
}
