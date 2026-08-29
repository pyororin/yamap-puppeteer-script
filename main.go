package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	kb "github.com/chromedp/chromedp/kb"
	"github.com/joho/godotenv"
)

// ActivityInfo holds the essential details for processing a post.
type ActivityInfo struct {
	URL     string
	Reacted bool
}

func main() {
	// コマンドライン引数の解析
	action := flag.String("action", "", "実行するアクション (例: react-timeline)")
	targetURL := flag.String("url", "", "処理・調査する活動記録のURL (react-activities / debug-activity-page で使用)")
	const availableActions = "利用可能なアクション: react-timeline, react-activities, follow-back, " +
		"list-non-mutual, unfollow-non-mutual, debug-follow-buttons, debug-activity-page"
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("警告: .envファイルが見つからないか、読み込みに失敗しました。")
	}

	switch *action {
	case "react-timeline":
		log.Println("アクション: react-timeline を実行します。")
		runTimelineReaction()
	case "react-activities":
		log.Println("アクション: react-activities を実行します。")
		runActivitiesReaction(*targetURL)
	case "follow-back":
		log.Println("アクション: follow-back を実行します。")
		runFollowBack()
	case "list-non-mutual":
		log.Println("アクション: list-non-mutual を実行します。")
		runListNonMutual()
	case "unfollow-non-mutual":
		log.Println("アクション: unfollow-non-mutual を実行します。")
		runUnfollowNonMutual()
	case "debug-follow-buttons":
		log.Println("アクション: debug-follow-buttons を実行します。")
		runDebugFollowButtons()
	case "debug-activity-page":
		log.Println("アクション: debug-activity-page を実行します。")
		runDebugActivityPage(*targetURL)
	case "":
		log.Println("エラー: -actionフラグが指定されていません。実行するアクションを指定してください。")
		log.Println(availableActions)
		os.Exit(1)
	default:
		log.Printf("エラー: 不明なアクション '%s' が指定されました。\n", *action)
		log.Println(availableActions)
		os.Exit(1)
	}
}

// runActivitiesReaction は活動一覧ページへのリアクション処理全体を実行する
func runActivitiesReaction(singleURL string) {
	log.Println("--- プログラム開始 (react-activities) ---")
	startTime := time.Now()

	log.Println("標準のchromedpを使用してヘッドレスブラウザを初期化しています...")
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancelAllocator()

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
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	log.Println("ブラウザの初期化完了。")

	log.Println("環境変数を読み込んでいます...")
	email := os.Getenv("YAMAP_EMAIL")
	password := os.Getenv("YAMAP_PASSWORD")
	postCountStr := os.Getenv("ACTIVITIES_POST_COUNT_TO_PROCESS")
	if email == "" || password == "" || postCountStr == "" {
		log.Fatal("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD, ACTIVITIES_POST_COUNT_TO_PROCESS を設定してください。")
	}
	postCount, err := strconv.Atoi(postCountStr)
	if err != nil {
		log.Fatalf("ACTIVITIES_POST_COUNT_TO_PROCESSの値が不正です: %v", err)
	}
	log.Println("環境変数の読み込み完了。")

	log.Println("ログイン処理を開始します...")
	loginStartTime := time.Now()
	// login関数はタイムラインへの遷移をハードコーディングしているので、ここではfalseを渡して遷移をスキップさせる
	if err := login(ctx, email, password, false); err != nil {
		log.Fatalf("ログインに失敗しました: %v", err)
	}
	log.Printf("ログイン成功。処理時間: %s", time.Since(loginStartTime))

	log.Println("活動一覧ページの処理を開始します...")
	activitiesStartTime := time.Now()
	var reactedURLs []string
	if singleURL != "" {
		// -url 指定時はその1件だけを処理する（動作確認・デバッグ用）
		log.Printf("-url が指定されたため、1件のみ処理します: %s", singleURL)
		if ok, sErr := sendReaction(ctx, singleURL); sErr != nil {
			log.Printf("リアクション処理でエラーが発生しました (%s): %v", singleURL, sErr)
		} else if ok {
			reactedURLs = append(reactedURLs, singleURL)
		}
	} else {
		reactedURLs, err = processActivities(ctx, postCount)
	}
	if err != nil {
		log.Printf("活動一覧ページの処理中にエラーが発生しました: %v", err)
	}
	log.Printf("活動一覧ページの処理完了。処理時間: %s", time.Since(activitiesStartTime))

	if len(reactedURLs) > 0 {
		log.Println("\n--- 「いいね！」した投稿一覧 ---")
		for _, url := range reactedURLs {
			log.Println(url)
		}
		log.Println("---------------------------------")
	}

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("総処理時間: %s", time.Since(startTime))

	printDependencies()
}

// processActivities は活動一覧ページを処理してリアクションを送信する
func processActivities(ctx context.Context, postCountToProcess int) ([]string, error) {
	var activityURLs []string
	seenURLs := make(map[string]struct{})
	page := 1
	consecutiveEmptyPages := 0

	log.Println("活動一覧ページから投稿URLを収集します...")
	for len(activityURLs) < postCountToProcess {
		// コンテキストがキャンセルされたかチェック
		if ctx.Err() != nil {
			log.Println("URL収集中にコンテキストがキャンセルされました。")
			break
		}

		pageURL := fmt.Sprintf("https://yamap.com/search/activities?page=%d", page)
		log.Printf("%dページ目に移動します: %s", page, pageURL)

		var nodes []*cdp.Node
		// ページ遷移のコンテキストにタイムアウトを設定
		pageCtx, pageCancel := context.WithTimeout(ctx, 30*time.Second)
		defer pageCancel()

		// ページに移動し、フッターが表示されるのを待つ（フッターはどのページにもあるため）
		err := chromedp.Run(pageCtx,
			chromedp.Navigate(pageURL),
			chromedp.WaitVisible(`footer[data-global-footer="true"]`),
		)
		if err != nil {
			log.Printf("%dページ目への移動または待機に失敗しました: %v", page, err)
			// タイムアウトなどの場合、次のページの試行は無意味なのでループを抜ける
			break
		}

		// ページに活動エントリが存在するかどうかを確認
		err = chromedp.Run(ctx,
			chromedp.Nodes(`[data-testid="activity-entry"] a[href^="/activities/"]`, &nodes, chromedp.ByQueryAll),
		)

		// エラーが発生した場合、またはノードが見つからない場合は、ページの終端と見なす
		if err != nil {
			log.Printf("%dページ目で活動エントリの取得に失敗しました。おそらく最終ページです: %v", page, err)
			break
		}
		if len(nodes) == 0 {
			log.Printf("%dページ目には活動が見つかりませんでした。", page)
			consecutiveEmptyPages++
			if consecutiveEmptyPages >= 3 {
				log.Println("3回連続で活動のないページに到達したため、収集を終了します。")
				break
			}
			page++
			continue // 次のページへ
		}

		// 新しいURLが見つかったので、連続空ページカウンターをリセット
		consecutiveEmptyPages = 0

		initialCount := len(activityURLs)
		for _, node := range nodes {
			url := "https://yamap.com" + node.AttributeValue("href")
			if _, seen := seenURLs[url]; !seen {
				seenURLs[url] = struct{}{}
				activityURLs = append(activityURLs, url)
				log.Printf("投稿URLを発見: %s (現在 %d 件)", url, len(activityURLs))
				if len(activityURLs) >= postCountToProcess {
					goto collected // 目標件数に達したので収集ループを抜ける
				}
			}
		}

		// このページで新しいURLが一つも見つからなかった場合
		if len(activityURLs) == initialCount {
			log.Println("このページでは新しいURLが見つかりませんでした。重複ページまたは最終ページと判断し、収集を終了します。")
			break
		}

		page++
		time.Sleep(2 * time.Second) // サーバーへの負荷を考慮した待機
	}

collected:
	log.Printf("%d件の投稿URLを収集しました。リアクション処理を開始します。", len(activityURLs))
	var reactedURLs []string
	for i, url := range activityURLs {
		log.Printf("--- 投稿 %d/%d を処理中 ---", i+1, len(activityURLs))
		liked, err := sendReaction(ctx, url)
		if err != nil {
			log.Printf("リアクション処理でエラーが発生しました (%s): %v", url, err)
		}
		if liked {
			reactedURLs = append(reactedURLs, url)
			log.Printf("いいね！しました。(現在 %d/%d 件)", len(reactedURLs), len(activityURLs))
		}
		if ctx.Err() != nil {
			log.Println("メインコンテキストがキャンセルされたため、リアクション処理を中断します。")
			break
		}
		time.Sleep(2 * time.Second)
	}

	log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", len(reactedURLs))
	return reactedURLs, nil
}

// runTimelineReaction はタイムラインへのリアクション処理全体を実行する
func runTimelineReaction() {
	log.Println("--- プログラム開始 ---")
	startTime := time.Now()

	log.Println("標準のchromedpを使用してヘッドレスブラウザを初期化しています...")
	// 多数の投稿を処理する際にブラウザセッションがタイムアウトしないよう、アロケータのタイムアウトを60分に延長
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancelAllocator()

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
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	// メインのコンテキストタイムアウトは넉넉하게設定
	ctx, cancel = context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	log.Println("ブラウザの初期化完了。")

	log.Println("環境変数を読み込んでいます...")
	email := os.Getenv("YAMAP_EMAIL")
	password := os.Getenv("YAMAP_PASSWORD")
	postCountStr := os.Getenv("TIMELINE_POST_COUNT_TO_PROCESS")
	if email == "" || password == "" || postCountStr == "" {
		log.Fatal("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD, TIMELINE_POST_COUNT_TO_PROCESS を設定してください。")
	}
	postCount, err := strconv.Atoi(postCountStr)
	if err != nil {
		log.Fatalf("TIMELINE_POST_COUNT_TO_PROCESSの値が不正です: %v", err)
	}
	log.Println("環境変数の読み込み完了。")

	log.Println("ログイン処理を開始します...")
	loginStartTime := time.Now()
	if err := login(ctx, email, password, true); err != nil {
		log.Fatalf("ログインに失敗しました: %v", err)
	}
	log.Printf("ログイン成功。処理時間: %s", time.Since(loginStartTime))

	log.Println("タイムラインの処理を開始します...")
	timelineStartTime := time.Now()
	reactedURLs, err := processTimeline(ctx, postCount)
	if err != nil {
		log.Printf("タイムライン処理中にエラーが発生しました: %v", err)
	}
	log.Printf("タイムライン処理完了。処理時間: %s", time.Since(timelineStartTime))

	if len(reactedURLs) > 0 {
		log.Println("\n--- 「いいね！」した投稿一覧 ---")
		for _, url := range reactedURLs {
			log.Println(url)
		}
		log.Println("---------------------------------")
	}

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("総処理時間: %s", time.Since(startTime))

	printDependencies()
}

func login(ctx context.Context, email, password string, navigateToTimeline bool) error {
	log.Println("ログインページに移動し、フォームを入力します...")
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1920, 1080),
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		chromedp.Click(`input[name="email"]`),
		chromedp.SendKeys(`input[name="email"]`, email),
		chromedp.Click(`input[name="password"]`),
		chromedp.SendKeys(`input[name="password"]`, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	log.Println("ログインボタンをクリックします...")

	actions := []chromedp.Action{
		chromedp.Evaluate(`document.evaluate("//button[span[text()='ログイン']]", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue.click()`, nil),
		// サーバーからの応答とリダイレクトを待つために少し待機
		chromedp.Sleep(10 * time.Second),
	}

	if navigateToTimeline {
		log.Println("明示的にタイムラインへ移動します...")
		actions = append(actions,
			chromedp.Navigate("https://yamap.com/timeline"),
			chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
		)
	} else {
		log.Println("ログイン成功を確認するため、少し待機します...")
		actions = append(actions,
			chromedp.Sleep(5*time.Second),
		)
	}

	if err := chromedp.Run(ctx, actions...); err != nil {
		log.Println("ログイン後のページ遷移または要素の表示確認に失敗しました。デバッグ情報を保存します...")
		var buf []byte
		var htmlContent string
		// スクリーンショットとHTMLを取得
		if dbgErr := chromedp.Run(ctx,
			chromedp.FullScreenshot(&buf, 90),
			chromedp.OuterHTML("html", &htmlContent),
		); dbgErr != nil {
			log.Printf("デバッグ情報（スクリーンショット/HTML）の取得に失敗: %v", dbgErr)
		} else {
			if wErr := os.WriteFile("login_failure_screenshot.png", buf, 0644); wErr != nil {
				log.Printf("スクリーンショットの保存に失敗: %v", wErr)
			} else {
				log.Println("スクリーンショットを login_failure_screenshot.png に保存しました。")
			}
			if wErr := os.WriteFile("login_failure.html", []byte(htmlContent), 0644); wErr != nil {
				log.Printf("HTMLの保存に失敗: %v", wErr)
			} else {
				log.Println("HTMLを login_failure.html に保存しました。")
			}
		}
		return fmt.Errorf("ログイン後の処理に失敗: %w", err)
	}

	log.Println("ログイン成功を確認しました。")
	return nil
}

func processTimeline(ctx context.Context, postCountToProcess int) ([]string, error) {
	log.Println("タイムライン上の投稿URLを収集します...")

	var activitiesToProcess []ActivityInfo
	seenURLs := make(map[string]struct{})
	var lastHeight int64
	noNewContentCount := 0

	// YAMAPのフロントエンド刷新により window.__NUXT__ は存在しなくなったため、
	// タイムラインのDOMから活動記録へのリンクを直接集める。
	// リアクション済みかどうかはここでは判定できないが、
	// sendReaction 側でピッカーを開いた時点で判定しスキップする。
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/timeline"),
		chromedp.WaitVisible(timelineActivityLink, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("タイムラインの読み込みに失敗: %w", err)
	}

	for len(activitiesToProcess) < postCountToProcess {
		select {
		case <-ctx.Done():
			log.Println("URL収集中にタイムアウトしました。")
			return nil, ctx.Err()
		default:
		}

		var hrefs []string
		if err := chromedp.Run(ctx, chromedp.Evaluate(`
			Array.from(new Set(
				Array.from(document.querySelectorAll('a[href^="/activities/"]'))
					.map(function(a){ return (a.getAttribute('href') || '').split('?')[0]; })
					.filter(function(h){ return /^\/activities\/\d+$/.test(h); })
			))
		`, &hrefs)); err != nil {
			log.Printf("投稿リンクの収集に失敗しました: %v", err)
			break
		}

		initialCount := len(activitiesToProcess)
		for _, h := range hrefs {
			url := "https://yamap.com" + h
			if _, seen := seenURLs[url]; seen {
				continue
			}
			seenURLs[url] = struct{}{}
			activitiesToProcess = append(activitiesToProcess, ActivityInfo{URL: url})
			log.Printf("投稿を発見: %s (現在 %d 件)", url, len(activitiesToProcess))
			if len(activitiesToProcess) >= postCountToProcess {
				goto collected
			}
		}

		if len(activitiesToProcess) == initialCount {
			noNewContentCount++
		} else {
			noNewContentCount = 0
		}

		if noNewContentCount >= 5 {
			log.Println("5回連続で新しい投稿が読み込まれませんでした。タイムラインの終端と判断します。")
			break
		}

		var currentHeight int64
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.scrollHeight`, &currentHeight)); err != nil {
			log.Printf("ページの高さの取得に失敗: %v", err)
			break
		}
		if currentHeight == lastHeight {
			log.Println("ページの高さが変わりませんでした。タイムラインの終端に到達した可能性があります。")
			noNewContentCount++
		}
		lastHeight = currentHeight

		log.Println("ページを下にスクロールします...")
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil)); err != nil {
			log.Printf("ページスクロールに失敗: %v", err)
			break
		}
		time.Sleep(5 * time.Second)
	}

collected:
	log.Printf("%d件の投稿を収集しました。リアクション処理を開始します。", len(activitiesToProcess))

	var reactedURLs []string
	for i, activity := range activitiesToProcess {
		log.Printf("--- 投稿 %d/%d を処理中 ---", i+1, len(activitiesToProcess))
		liked, err := sendReaction(ctx, activity.URL)
		if err != nil {
			log.Printf("リアクション処理でエラーが発生しました (%s): %v", activity.URL, err)
		}
		if liked {
			reactedURLs = append(reactedURLs, activity.URL)
			log.Printf("いいね！しました。(現在 %d/%d 件)", len(reactedURLs), len(activitiesToProcess))
		}
		// メインのコンテキストがキャンセルされた場合は、ループを中断
		if ctx.Err() != nil {
			log.Println("メインコンテキストがキャンセルされたため、リアクション処理を中断します。")
			break
		}
		time.Sleep(2 * time.Second) // 連続アクセスを避けるための待機
	}

	log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", len(reactedURLs))
	return reactedURLs, nil
}

// リアクション関連のセレクタ。
// YAMAPのフロントエンドがNuxtからNext.jsへ刷新され、
// CSSクラス名がEmotionのハッシュ（css-xxxxxx）になったため、
// クラス名ではなく aria-label を手がかりにする。
const (
	// 投稿ページのリアクション追加ボタン
	reactionOpenButton = `button[aria-label="絵文字をおくる"]`
	// リアクション数を表示するリンク。aria-labelは「絵文字をおくった人（N件の絵文字）」
	reactionCountLink = `a[aria-label^="絵文字をおくった人"]`
	// 送信する絵文字。ピッカー内の並び順ではなく aria-label で指定する
	reactionEmojiLabel = "thumbs up"
	// タイムライン上の活動記録へのリンク
	timelineActivityLink = `a[href^="/activities/"]`
)

// readReactionCount は投稿に付いているリアクション数を読み取る。
// 取得できない場合は -1 を返す。
func readReactionCount(ctx context.Context) int {
	var count int
	script := `
		(function() {
			var a = document.querySelector('a[aria-label^="絵文字をおくった人"]');
			if (!a) return -1;
			var m = (a.getAttribute('aria-label') || '').match(/（(\d+)件/);
			if (m) return parseInt(m[1], 10);
			var t = (a.innerText || '').match(/(\d+)/);
			return t ? parseInt(t[1], 10) : -1;
		})()
	`
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &count)); err != nil {
		return -1
	}
	return count
}

// viewerHasReacted は自分が既にこの投稿へリアクション済みかを返す。
// リアクション済みの絵文字には class="emoji-button viewer-has-reacted" が付き、
// aria-label が「<絵文字名> 削除する」に変わる。
//
// この印は絵文字ピッカーを開いて初めて描画されるため、
// 必ずピッカーを開いた状態で呼ぶこと。
func viewerHasReacted(ctx context.Context) (bool, error) {
	var reacted bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`!!document.querySelector('.viewer-has-reacted')`, &reacted)); err != nil {
		return false, fmt.Errorf("リアクション済み判定の評価に失敗: %w", err)
	}
	return reacted, nil
}

// sendReaction は投稿ページを開き、絵文字リアクションを送信する。
func sendReaction(parentCtx context.Context, url string) (bool, error) {
	reactionCtx, cancel := context.WithTimeout(parentCtx, 90*time.Second)
	defer cancel()

	log.Printf("投稿ページに移動してリアクションを送信します: %s", url)

	// リアクションボタンの出現をもって「ページが使える状態」と判断する
	if err := chromedp.Run(reactionCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(reactionOpenButton, chromedp.ByQuery),
	); err != nil {
		log.Println("リアクションページの基本読み込みに失敗しました。")
		return false, fmt.Errorf("投稿ページの基本読み込みに失敗: %w", err)
	}

	before := readReactionCount(reactionCtx)
	log.Printf("送信前のリアクション数: %d", before)

	emojiSelector := fmt.Sprintf(`[role="dialog"] button[aria-label=%s]`, quoteJS(reactionEmojiLabel))

	var sendErr error
	for i := 0; i < 3; i++ {
		log.Printf("リアクション試行 %d回目: %s", i+1, url)

		// ピッカーを開く
		if err := chromedp.Run(reactionCtx,
			chromedp.ScrollIntoView(reactionOpenButton, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.Click(reactionOpenButton, chromedp.ByQuery),
			chromedp.WaitVisible(`[role="dialog"]`, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
		); err != nil {
			log.Printf("絵文字ピッカーの表示に失敗: %v", err)
			sendErr = err
			continue
		}

		// 既に自分がリアクション済みなら触らない。
		// 同じ絵文字を再度クリックするとトグルで解除されてしまう。
		// この印はピッカーを開いて初めて描画されるため、ここで判定する。
		if reacted, err := viewerHasReacted(reactionCtx); err != nil {
			log.Printf("リアクション済み判定に失敗しました。処理を継続します: %v", err)
		} else if reacted {
			log.Printf("既にリアクション済みのためスキップします: %s", url)
			// ピッカーを閉じてから抜ける
			_ = chromedp.Run(reactionCtx, chromedp.KeyEvent(kb.Escape), chromedp.Sleep(1*time.Second))
			return false, nil
		}

		// 絵文字を選択する
		log.Printf("絵文字 %q を選択します。", reactionEmojiLabel)
		sendErr = chromedp.Run(reactionCtx,
			chromedp.Click(emojiSelector, chromedp.ByQuery),
			chromedp.Sleep(3*time.Second),
		)

		if sendErr == nil {
			// リアクション数が増えたかで送信成功を検証する。
			// 既に同じ絵文字を送っていた場合はトグルで解除され数が減るため、
			// 増加を確認できなければ失敗として扱う。
			after := readReactionCount(reactionCtx)
			log.Printf("送信後のリアクション数: %d", after)
			if before >= 0 && after >= 0 && after <= before {
				log.Printf("リアクション数が増えていません（%d→%d）。既にリアクション済みの可能性があります: %s", before, after, url)
				return false, nil
			}
			log.Printf("リアクションの送信に成功しました: %s", url)
			return true, nil
		}

		log.Printf("試行 %d回目が失敗しました (%s): %v", i+1, url, sendErr)

		if reactionCtx.Err() != nil {
			log.Printf("コンテキストエラーのためリアクション処理を中断します: %v", reactionCtx.Err())
			break
		}

		if i < 2 {
			log.Println("ページをリロードして再試行します...")
			if err := chromedp.Run(reactionCtx,
				chromedp.Reload(),
				chromedp.WaitVisible(reactionOpenButton, chromedp.ByQuery),
			); err != nil {
				log.Printf("リロードに失敗: %v", err)
				return false, fmt.Errorf("リロード後のボタン待機に失敗: %w", err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	return false, fmt.Errorf("リアクションの送信に失敗しました（3回試行）: %w", sendErr)
}

// runFollowBack はフォロワーのフォローバック処理全体を実行する
func runFollowBack() {
	log.Println("--- プログラム開始 (follow-back) ---")
	startTime := time.Now()

	log.Println("標準のchromedpを使用してヘッドレスブラウザを初期化しています...")
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancelAllocator()

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
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	log.Println("ブラウザの初期化完了。")

	log.Println("環境変数を読み込んでいます...")
	email := os.Getenv("YAMAP_EMAIL")
	password := os.Getenv("YAMAP_PASSWORD")
	if email == "" || password == "" {
		log.Fatal("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD を設定してください。")
	}
	log.Println("環境変数の読み込み完了。")

	log.Println("ログイン処理を開始します...")
	loginStartTime := time.Now()
	if err := login(ctx, email, password, false); err != nil {
		log.Fatalf("ログインに失敗しました: %v", err)
	}
	log.Printf("ログイン成功。処理時間: %s", time.Since(loginStartTime))

	log.Println("フォロワーのフォローバック処理を開始します...")
	followBackStartTime := time.Now()

	// ログインしているユーザーのIDを自動取得
	userID, err := getMyUserID(ctx)
	if err != nil {
		log.Printf("ユーザーIDの取得に失敗しました。デフォルトのIDを使用します: %v", err)
		userID = "278948" // フォールバック
	}
	log.Printf("ログイン中のユーザーID: %s", userID)

	followedUsers, err := processFollowBack(ctx, userID)
	if err != nil {
		log.Printf("フォローバック処理中にエラーが発生しました: %v", err)
	}
	log.Printf("フォローバック処理完了。処理時間: %s", time.Since(followBackStartTime))

	if len(followedUsers) > 0 {
		log.Println("\n--- フォローバックしたユーザー一覧 ---")
		for _, user := range followedUsers {
			log.Println(user)
		}
		log.Println("--------------------------------------")
	} else {
		log.Println("フォローバックが必要なユーザーはいませんでした。")
	}

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("総処理時間: %s", time.Since(startTime))

	printDependencies()
}

// processFollowBack はフォロワーページを巡回し、未フォローのフォロワーをフォローバックする
func processFollowBack(ctx context.Context, userID string) ([]string, error) {
	followersPageURL := fmt.Sprintf("https://yamap.com/users/%s?tab=followers#tabs", userID)
	var followedUsers []string
	pageNum := 1

	log.Println("フォロワーページに移動します...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate(followersPageURL),
		chromedp.Sleep(5*time.Second),  // Wait instead of WaitVisible for simplicity and robust
		chromedp.Sleep(10*time.Second), // コンテンツの完全な描画を待機
	); err != nil {
		return nil, fmt.Errorf("フォロワーページへの移動に失敗しました: %w", err)
	}
	log.Println("フォロワーページの読み込み完了。")

	// ページごとにフォローバック対象を処理するループ
	for {
		if ctx.Err() != nil {
			log.Println("コンテキストがキャンセルされたため、処理を中断します。")
			break
		}

		log.Printf("--- %dページ目のフォロワーを処理中 ---", pageNum)

		// ページ内のフォローバック対象ユーザーのインデックスを取得する
		// JavaScriptでDOM解析を行い、「フォローされています」テキストがあり、
		// かつ「フォローする」ボタンがあるカードのインデックスを返す
		var targetIndicesJSON string
		err := chromedp.Run(ctx,
			chromedp.Evaluate(`
				(function() {
					var cards = document.querySelectorAll('article');
					var targets = [];
					for (var i = 0; i < cards.length; i++) {
						var card = cards[i];
						var cardText = card.innerText;
						var hasFollowedBy = cardText.includes('フォローされています');

						var followButton = null;
						var buttons = card.querySelectorAll('button');
						for (var j = 0; j < buttons.length; j++) {
							var btnText = buttons[j].innerText.trim();
							if (btnText === 'フォローする') {
								followButton = buttons[j];
								break;
							}
						}

						// デバッグ情報収集
						var nameEl = card.querySelector('a[href^="/users/"]');
						var userName = nameEl ? nameEl.innerText.trim() : '不明';
						var userLink = nameEl ? nameEl.getAttribute('href') : '';

						// 「フォローされています」マークがあり、かつ「フォローする」ボタンがある場合のみ対象にする
						if (hasFollowedBy && followButton) {
							targets.push({index: i, name: userName, href: userLink});
						}
					}
					return JSON.stringify(targets);
				})()
			`, &targetIndicesJSON),
		)
		if err != nil {
			log.Printf("フォロワーカードの解析に失敗しました: %v", err)
			break
		}

		// JSONパース
		type FollowTarget struct {
			Index int    `json:"index"`
			Name  string `json:"name"`
			Href  string `json:"href"`
		}
		var targets []FollowTarget
		if err := json.Unmarshal([]byte(targetIndicesJSON), &targets); err != nil {
			log.Printf("フォロワーターゲットのJSONパースに失敗しました: %v", err)
			break
		}

		// デバッグ用に検出されたカードの総数も取得
		var totalCards int
		chromedp.Run(ctx, chromedp.Evaluate(`document.querySelectorAll('div[data-testid="user"]').length`, &totalCards))
		log.Printf("%dページ目: 全%d件中、%d件のフォローバック対象を発見しました。", pageNum, totalCards, len(targets))

		// 各対象ユーザーの「フォローする」ボタンをクリックする
		// 注意: ボタンをクリックすると「フォロー中」に変わるため、インデックスは変わらないが
		// DOMが更新されるので、毎回最新のDOMからボタンを探す
		for _, target := range targets {
			if ctx.Err() != nil {
				log.Println("コンテキストがキャンセルされたため、処理を中断します。")
				break
			}

			log.Printf("フォローバック中: %s (https://yamap.com%s)", target.Name, target.Href)

			// JavaScriptで特定のカード内の「フォローする」ボタンをクリック
			var clickResult bool
			clickErr := chromedp.Run(ctx,
				chromedp.Evaluate(fmt.Sprintf(`
					(function() {
						var cards = document.querySelectorAll('article');
						if (cards.length <= %d) return false;
						var card = cards[%d];
						var buttons = card.querySelectorAll('button');
						for (var j = 0; j < buttons.length; j++) {
							if (buttons[j].innerText.trim() === 'フォローする') {
								buttons[j].click();
								return true;
							}
						}
						return false;
					})()
				`, target.Index, target.Index), &clickResult),
				chromedp.Sleep(2*time.Second),
			)

			if clickErr != nil {
				log.Printf("フォローボタンのクリックに失敗しました (%s): %v", target.Name, clickErr)
				continue
			}

			if clickResult {
				userURL := fmt.Sprintf("https://yamap.com%s", target.Href)
				followedUsers = append(followedUsers, fmt.Sprintf("%s (%s)", target.Name, userURL))
				log.Printf("フォローバック成功: %s (現在 %d 件)", target.Name, len(followedUsers))
			} else {
				log.Printf("フォローボタンが見つかりませんでした: %s", target.Name)
			}

			// 連続リクエストを避けるための待機
			time.Sleep(1 * time.Second)
		}

		// 次のページへ進む
		log.Println("次のページがあるか確認します...")
		var hasNextPage bool
		err = chromedp.Run(ctx,
			chromedp.Evaluate(`
				(function() {
					var nextBtn = document.querySelector('button[aria-label="次のページに移動する"]');
					return nextBtn !== null && !nextBtn.disabled;
				})()
			`, &hasNextPage),
		)
		if err != nil {
			log.Printf("次のページボタンの確認に失敗しました: %v", err)
			break
		}

		if !hasNextPage {
			log.Println("最後のページに到達しました。フォローバック処理を終了します。")
			break
		}

		// 次のページボタンをクリック
		log.Println("次のページに移動します...")
		err = chromedp.Run(ctx,
			chromedp.Click(`button[aria-label="次のページに移動する"]`, chromedp.ByQuery),
			chromedp.Sleep(5*time.Second), // Sleep instead of WaitVisible for reliability
		)
		if err != nil {
			log.Printf("次のページへの移動に失敗しました: %v", err)
			break
		}

		pageNum++
	}

	log.Printf("フォローバック処理が完了しました。合計 %d 人をフォローバックしました。", len(followedUsers))
	return followedUsers, nil
}

// getMyUserID はログイン後のセッションから現在のユーザーIDを取得します
func getMyUserID(ctx context.Context) (string, error) {
	var userID string
	log.Println("ユーザーIDを取得するためにマイページに移動します...")
	var currentURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/mypage"),
		chromedp.Sleep(5*time.Second),
		chromedp.Location(&currentURL),
	)
	if err == nil {
		// e.g. https://yamap.com/users/123456
		if match := regexp.MustCompile(`.*/users/(\d+)`).FindStringSubmatch(currentURL); len(match) > 1 {
			userID = match[1]
		}
	}
	if userID == "" {
		// As a fallback, try to parse from the menu or header links
		_ = chromedp.Run(ctx, chromedp.Evaluate(`
			(function() {
				var links = document.querySelectorAll('a[href^="/users/"]');
				for (var i = 0; i < links.length; i++) {
					var href = links[i].getAttribute('href');
					var match = href.match(/^\/users\/(\d+)$/);
					if (match) return match[1];
				}
				return null;
			})()
		`, &userID))
	}

	if err != nil || userID == "" {
		return "", fmt.Errorf("マイページからユーザーIDを取得できませんでした: %w (currentURL: %s)", err, currentURL)
	}

	return userID, nil
}

// printDependencies は go.mod ファイルを解析し、直接の依存関係を標準出力に表示します。
func printDependencies() {
	file, err := os.Open("go.mod")
	if err != nil {
		log.Printf("go.modファイルの読み込みに失敗しました: %v", err)
		return
	}
	defer file.Close()

	log.Println("\n--- このプログラムの実行に必要だったライブラリ一覧 ---")
	scanner := bufio.NewScanner(file)
	inRequireBlock := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "require (" {
			inRequireBlock = true
			continue
		}
		if line == ")" {
			inRequireBlock = false
			continue
		}
		// requireブロック内にあり、コメントではない、かつ空行でもない行を処理
		if inRequireBlock && !strings.HasPrefix(line, "//") && line != "" {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// 間接的な依存関係 "// indirect" を含まないもののみ出力
				if !strings.HasSuffix(line, "// indirect") {
					log.Printf("- %s %s", parts[0], parts[1])
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("go.modファイルのスキャン中にエラーが発生しました: %v", err)
	}
	log.Println("----------------------------------------------------")
}
