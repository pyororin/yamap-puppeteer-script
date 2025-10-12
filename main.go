package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/joho/godotenv"
)

// ActivityInfo holds the essential details for processing a post.
type ActivityInfo struct {
	URL     string
	Reacted bool
}

// Activity represents the activity data within a feed item.
type Activity struct {
	ID             int64 `json:"id"`
	EmojiReactions []struct {
		ViewerHasReacted bool `json:"viewer_has_reacted"`
	} `json:"emoji_reactions"`
}

// Journal represents a journal entry within a feed item.
// It's kept minimal as we only need it for parsing.
type Journal struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

// FeedItem represents a single item in the timeline feed.
// It includes fields for both activities and journals to ensure proper JSON parsing.
type FeedItem struct {
	ID           int64     `json:"id"`
	FeedableType string    `json:"feedable_type"`
	Activity     *Activity `json:"activity"`
	Journal      *Journal  `json:"journal"`
}

// parseNuxtData extracts and parses the timeline feed data from the page's javascript context.
func parseNuxtData(ctx context.Context) ([]FeedItem, error) {
	var res json.RawMessage
	// タイムラインのフィードは window.__NUXT__.state.timeline.feeds に格納されている
	// オブジェクトを直接返し、chromedpにJSONへのシリアライズを任せる
	script := `
		(function() {
			if (window.__NUXT__ && window.__NUXT__.state && window.__NUXT__.state.timeline && window.__NUXT__.state.timeline.feeds) {
				return window.__NUXT__.state.timeline.feeds;
			}
			return null;
		})();
	`
	err := chromedp.Run(ctx,
		chromedp.Evaluate(script, &res),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate javascript to get feed items: %w", err)
	}

	if len(res) == 0 || string(res) == "null" {
		return []FeedItem{}, nil
	}

	var items []FeedItem
	if err := json.Unmarshal(res, &items); err != nil {
		os.WriteFile("failed_unmarshal_feeds.json", res, 0644)
		return nil, fmt.Errorf("failed to unmarshal feed items from javascript object: %w. JSON saved to failed_unmarshal_feeds.json", err)
	}

	return items, nil
}


func main() {
	// コマンドライン引数の解析
	action := flag.String("action", "", "実行するアクション (例: react-timeline)")
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
		runActivitiesReaction()
	case "":
		log.Println("エラー: -actionフラグが指定されていません。実行するアクションを指定してください。")
		log.Println("利用可能なアクション: react-timeline, react-activities")
		os.Exit(1)
	default:
		log.Printf("エラー: 不明なアクション '%s' が指定されました。\n", *action)
		log.Println("利用可能なアクション: react-timeline, react-activities")
		os.Exit(1)
	}
}

// runActivitiesReaction は活動一覧ページへのリアクション処理全体を実行する
func runActivitiesReaction() {
	log.Println("--- プログラム開始 (react-activities) ---")
	startTime := time.Now()

	log.Println("標準のchromedpを使用してヘッドレスブラウザを初期化しています...")
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancelAllocator()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Headless,
		chromedp.NoSandbox,
		chromedp.DisableGPU,
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
	postCountStr := os.Getenv("POST_COUNT_TO_PROCESS")
	if email == "" || password == "" || postCountStr == "" {
		log.Fatal("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD, POST_COUNT_TO_PROCESS を設定してください。")
	}
	postCount, err := strconv.Atoi(postCountStr)
	if err != nil {
		log.Fatalf("POST_COUNT_TO_PROCESSの値が不正です: %v", err)
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
	reactedURLs, err := processActivities(ctx, postCount)
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
	log.Println("活動一覧ページに移動します: https://yamap.com/search/activities")
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/search/activities"),
		chromedp.WaitVisible(`[data-testid="activity-entry"]`),
	); err != nil {
		return nil, fmt.Errorf("活動一覧ページの読み込みに失敗: %w", err)
	}

	var activityURLs []string
	seenURLs := make(map[string]struct{})
	var lastHeight int64
	noNewContentCount := 0

	log.Println("活動一覧ページから投稿URLを収集します...")
	for len(activityURLs) < postCountToProcess {
		var nodes []*cdp.Node
		if err := chromedp.Run(ctx,
			chromedp.Nodes(`[data-testid="activity-entry"] a[href^="/activities/"]`, &nodes, chromedp.ByQueryAll),
		); err != nil {
			log.Printf("URLノードの取得に失敗: %v", err)
			break
		}

		initialCount := len(activityURLs)
		for _, node := range nodes {
			url := "https://yamap.com" + node.AttributeValue("href")
			if _, seen := seenURLs[url]; !seen {
				seenURLs[url] = struct{}{}
				activityURLs = append(activityURLs, url)
				log.Printf("投稿URLを発見: %s (現在 %d 件)", url, len(activityURLs))
				if len(activityURLs) >= postCountToProcess {
					goto collected
				}
			}
		}
		if len(activityURLs) == initialCount {
			noNewContentCount++
		} else {
			noNewContentCount = 0
		}

		if noNewContentCount >= 5 {
			log.Println("5回連続で新しい投稿が読み込まれませんでした。ページの終端と判断します。")
			break
		}

		var currentHeight int64
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.scrollHeight`, &currentHeight)); err != nil {
			log.Printf("ページの高さの取得に失敗: %v", err)
			break
		}
		if currentHeight == lastHeight {
			log.Println("ページの高さが変わりませんでした。ページの終端に到達した可能性があります。")
			noNewContentCount++
		}
		lastHeight = currentHeight

		log.Println("ページを下にスクロールします...")
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil)); err != nil {
			log.Printf("ページスクロールに失敗: %v", err)
			break
		}
		time.Sleep(3 * time.Second) // 新しいコンテンツが読み込まれるのを待つ
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
	postCountStr := os.Getenv("POST_COUNT_TO_PROCESS")
	if email == "" || password == "" || postCountStr == "" {
		log.Fatal("環境変数 YAMAP_EMAIL, YAMAP_PASSWORD, POST_COUNT_TO_PROCESS を設定してください。")
	}
	postCount, err := strconv.Atoi(postCountStr)
	if err != nil {
		log.Fatalf("POST_COUNT_TO_PROCESSの値が不正です: %v", err)
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
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		chromedp.SendKeys(`input[name="email"]`, email),
		chromedp.SendKeys(`input[name="password"]`, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	log.Println("ログインボタンをクリックします...")
	loginCtx, loginCancel := context.WithTimeout(ctx, 60*time.Second)
	defer loginCancel()

	actions := []chromedp.Action{
		chromedp.Evaluate(`document.querySelector('button[type="submit"]').click()`, nil),
		// サーバーからの応答とリダイレクトを待つために少し待機
		chromedp.Sleep(5 * time.Second),
	}

	if navigateToTimeline {
		log.Println("明示的にタイムラインへ移動します...")
		actions = append(actions,
			chromedp.Navigate("https://yamap.com/timeline"),
			chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
		)
	} else {
		log.Println("ログイン成功を確認するため、マイページリンクの表示を待ちます...")
		// ログイン後の汎用的な待機条件として、フッターが表示されるのを待つ
		actions = append(actions,
			chromedp.WaitVisible(`footer[data-global-footer="true"]`, chromedp.ByQuery),
		)
	}

	if err := chromedp.Run(loginCtx, actions...); err != nil {
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
	log.Println("タイムライン上の未リアクションの投稿URLを収集します...")

	var activitiesToProcess []ActivityInfo
	seenActivityIDs := make(map[int64]struct{})
	var lastHeight int64
	noNewContentCount := 0

	for len(activitiesToProcess) < postCountToProcess {
		select {
		case <-ctx.Done():
			log.Println("URL収集中にタイムアウトしました。")
			return nil, ctx.Err()
		default:
		}

		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
			chromedp.Poll(`window.__NUXT__ && window.__NUXT__.state && window.__NUXT__.state.timeline && window.__NUXT__.state.timeline.feeds`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		); err != nil {
			log.Printf("タイムラインデータの準備待機中にエラーが発生しました: %v", err)
			break // ループを抜けて収集したURLの処理に移る
		}

		feedItems, err := parseNuxtData(ctx)
		if err != nil {
			log.Printf("NUXTデータのパースに失敗: %v", err)
			break
		}

		initialCount := len(activitiesToProcess)
		for _, item := range feedItems {
			if item.Activity == nil || item.Activity.ID == 0 {
				continue
			}
			if _, seen := seenActivityIDs[item.Activity.ID]; !seen {
				seenActivityIDs[item.Activity.ID] = struct{}{}
				hasReacted := false
				for _, reaction := range item.Activity.EmojiReactions {
					if reaction.ViewerHasReacted {
						hasReacted = true
						break
					}
				}
				if !hasReacted {
					url := fmt.Sprintf("https://yamap.com/activities/%d", item.Activity.ID)
					activitiesToProcess = append(activitiesToProcess, ActivityInfo{URL: url})
					log.Printf("未リアクションの投稿を発見: %s (現在 %d 件)", url, len(activitiesToProcess))
					if len(activitiesToProcess) >= postCountToProcess {
						goto collected
					}
				}
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
	log.Printf("%d件の未リアクション投稿を収集しました。リアクション処理を開始します。", len(activitiesToProcess))

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

func sendReaction(parentCtx context.Context, url string) (bool, error) {
	reactionCtx, cancel := context.WithTimeout(parentCtx, 20*time.Minute)
	defer cancel()

	log.Printf("投稿ページに移動してリアクションを送信します: %s", url)

	if err := chromedp.Run(reactionCtx, chromedp.Navigate(url), chromedp.WaitVisible(`.FooterNav`, chromedp.ByQuery)); err != nil {
		log.Println("リアクションページの基本読み込みに失敗しました。")
		return false, fmt.Errorf("投稿ページの基本読み込みに失敗: %w", err)
	}

	if err := chromedp.Run(reactionCtx,
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
		chromedp.WaitVisible(`.emoji-add-button`, chromedp.ByQuery),
	); err != nil {
		log.Println("リアクションボタンの表示待機に失敗しました。")
		return false, fmt.Errorf("リアクションボタンの表示待機に失敗: %w", err)
	}

	var sendErr error
	for i := 0; i < 3; i++ {
		log.Printf("リアクション試行 %d回目: %s", i+1, url)

		if err := chromedp.Run(reactionCtx,
			chromedp.Click(`.emoji-add-button`, chromedp.ByQuery),
			chromedp.WaitVisible(`.emojiPickerBody`),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			log.Printf("絵文字ピッカーの表示に失敗: %v", err)
			sendErr = err
			continue
		}

		// 以前はリアクション済みの絵文字をクリックしようとしていたが、
		// 0件の場合はピッカーから選択する必要があるためロジックを修正。
		// ピッカー内の最初の絵文字ボタンをクリックする。
		log.Println("絵文字ピッカーから最初の絵文字を選択してクリックします。")

		sendErr = chromedp.Run(reactionCtx,
			// ご指摘のHTML構造に基づき、絵文字ピッカー内の最初のボタンをクリックするよう修正
			chromedp.Click(`.emojiPickerBody .emoji-button:first-child`, chromedp.ByQuery),
			chromedp.Sleep(3*time.Second), // Wait for the reaction to be sent
		)

		if sendErr == nil {
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
			if err := chromedp.Run(reactionCtx, chromedp.Reload(), chromedp.WaitVisible(`.emoji-add-button`)); err != nil {
				log.Printf("リロードに失敗: %v", err)
				return false, fmt.Errorf("リロード後のボタン待機に失敗: %w", err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	return false, fmt.Errorf("リアクションの送信に失敗しました（3回試行）: %w", sendErr)
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