package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

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
	if err := godotenv.Load(); err != nil {
		log.Println("警告: .envファイルが見つからないか、読み込みに失敗しました。")
	}

	log.Println("--- プログラム開始 ---")
	startTime := time.Now()

	log.Println("標準のchromedpを使用してヘッドレスブラウザを初期化しています...")
	// ログインやページ読み込みに時間がかかる場合を考慮し、アロケータのタイムアウトを5分に延長
	allocatorCtx, cancelAllocator := context.WithTimeout(context.Background(), 5*time.Minute)
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

	ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
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
	if err := login(ctx, email, password); err != nil {
		log.Fatalf("ログインに失敗しました: %v", err)
	}
	log.Printf("ログイン成功。処理時間: %s", time.Since(loginStartTime))

	log.Println("タイムラインの処理を開始します...")
	timelineStartTime := time.Now()
	if err := processTimeline(ctx, postCount); err != nil {
		log.Fatalf("タイムライン処理中に致命的なエラーが発生しました: %v", err)
	}
	log.Printf("タイムライン処理完了。処理時間: %s", time.Since(timelineStartTime))

	log.Printf("--- 全ての処理が正常に完了しました ---")
	log.Printf("総処理時間: %s", time.Since(startTime))
}

func login(ctx context.Context, email, password string) error {
	log.Println("ログインページに移動し、フォームを入力します...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		chromedp.SendKeys(`input[name="email"]`, email),
		chromedp.SendKeys(`input[name="password"]`, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	log.Println("ログインボタンをクリックし、明示的にタイムラインへ移動します...")
	loginCtx, loginCancel := context.WithTimeout(ctx, 60*time.Second)
	defer loginCancel()

	err := chromedp.Run(loginCtx,
		// 1. ログインボタンをクリック
		chromedp.Evaluate(`document.querySelector('button[type="submit"]').click()`, nil),
		// 2. サーバーからの応答とリダイレクトを待つために少し待機
		chromedp.Sleep(5*time.Second),
		// 3. セッションが確立された後、明示的にタイムラインページに移動
		chromedp.Navigate("https://yamap.com/timeline"),
		// 4. タイムラインフィードが表示されるまで待機
		chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
	)
	if err != nil {
		log.Println("ログイン後のタイムラインへの移動または表示確認に失敗しました。")
		var buf []byte
		if scrErr := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); scrErr != nil {
			log.Printf("スクリーンショットの取得に失敗: %v", scrErr)
		} else if wErr := os.WriteFile("login_failure_screenshot.png", buf, 0644); wErr != nil {
			log.Printf("スクリーンショットの保存に失敗: %v", wErr)
		}
		return fmt.Errorf("タイムラインへの移動または表示確認に失敗: %w", err)
	}

	log.Println("ログイン成功を確認しました。")
	return nil
}

func processTimeline(ctx context.Context, postCountToProcess int) error {
	log.Println("タイムラインの処理とリアクション送信を開始します。")

	seenActivityIDs := make(map[int64]struct{})
	var successfulReactions int
	var lastHeight int64
	noNewContentCount := 0
	timelineURL := "https://yamap.com/timeline"

	for successfulReactions < postCountToProcess {
		var activitiesToProcess []ActivityInfo
		var feedItems []FeedItem
		var err error

		select {
		case <-ctx.Done():
			log.Println("処理中にタイムアウトしました。")
			return ctx.Err()
		default:
		}

		var currentURL string
		chromedp.Run(ctx, chromedp.Location(&currentURL))
		if !strings.HasPrefix(currentURL, timelineURL) {
			log.Printf("タイムラインページ (%s) にいません。移動します...", timelineURL)
			if err := chromedp.Run(ctx, chromedp.Navigate(timelineURL), chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery)); err != nil {
				log.Printf("タイムラインへの復帰に失敗: %v", err)
				return err
			}
		}

		log.Println("タイムラインのJavaScriptデータの読み込みを待機します...")
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
			chromedp.Poll(`window.__NUXT__ && window.__NUXT__.state && window.__NUXT__.state.timeline && window.__NUXT__.state.timeline.feeds`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		); err != nil {
			log.Printf("タイムラインデータの準備待機中にタイムアウトまたはエラーが発生しました: %v。スクロールして再試行します。", err)
			var htmlContent string
			if dbgErr := chromedp.Run(ctx, chromedp.OuterHTML("html", &htmlContent)); dbgErr == nil {
				os.WriteFile("timeline_page_on_wait_error.html", []byte(htmlContent), 0644)
				log.Println("待機エラー発生時のHTMLを timeline_page_on_wait_error.html に保存しました。")
			}
			goto scroll
		}

		feedItems, err = parseNuxtData(ctx)
		if err != nil {
			log.Printf("NUXTデータのパースに失敗: %v。スクロールして再試行します。", err)
			goto scroll
		}

		if len(feedItems) > 0 {
			for _, item := range feedItems {
				// FeedにはActivity以外の項目(Journalなど)も含まれるため、Activityがnilでないことを確認
				if item.Activity == nil {
					continue
				}
				activity := *item.Activity // ポインタを実体に変換
				if activity.ID == 0 {
					continue
				}
				if _, seen := seenActivityIDs[activity.ID]; !seen {
					seenActivityIDs[activity.ID] = struct{}{}
					hasReacted := false
					if len(activity.EmojiReactions) > 0 {
						for _, reaction := range activity.EmojiReactions {
							if reaction.ViewerHasReacted {
								hasReacted = true
								break
							}
						}
					}
					if !hasReacted {
						url := fmt.Sprintf("https://yamap.com/activities/%d", activity.ID)
						activitiesToProcess = append(activitiesToProcess, ActivityInfo{URL: url})
						log.Printf("未リアクションの投稿を発見: %s", url)
					}
				}
			}
		}

		if len(activitiesToProcess) == 0 {
			noNewContentCount++
			log.Printf("新しい未リアクションの投稿が見つかりませんでした。(試行 %d/3)", noNewContentCount)
		} else {
			noNewContentCount = 0
			log.Printf("%d件の未リアクション投稿を処理します。", len(activitiesToProcess))
		}

		for _, activity := range activitiesToProcess {
			reactionCtx, reactionCancel := context.WithTimeout(ctx, 2*time.Minute)
			liked, err := sendReaction(reactionCtx, activity.URL, timelineURL)
			reactionCancel()

			if err != nil {
				log.Printf("リアクション処理でエラーが発生しました (%s): %v", activity.URL, err)
			}
			if liked {
				successfulReactions++
				log.Printf("いいね！しました。(現在 %d/%d 件)", successfulReactions, postCountToProcess)
				if successfulReactions >= postCountToProcess {
					log.Printf("目標の %d 件の「いいね！」を達成しました。", postCountToProcess)
					goto end
				}
			}
			time.Sleep(1 * time.Second)
		}

	scroll:
		if successfulReactions >= postCountToProcess {
			break
		}

		var currentHeight int64
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.scrollHeight`, &currentHeight)); err != nil {
			log.Printf("ページの高さの取得に失敗: %v", err)
			break
		}

		if noNewContentCount > 0 && currentHeight == lastHeight {
			if noNewContentCount >= 3 {
				log.Println("タイムラインの終端に到達したか、新しい投稿が読み込まれませんでした。処理を終了します。")
				break
			}
		}
		lastHeight = currentHeight

		log.Println("ページを下にスクロールします...")
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil)); err != nil {
			log.Printf("ページスクロールに失敗: %v", err)
			break
		}
		time.Sleep(5 * time.Second)
	}

end:
	log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", successfulReactions)
	return nil
}

func sendReaction(ctx context.Context, url, timelineURL string) (bool, error) {
	log.Printf("投稿ページに移動してリアクションを送信します: %s", url)
	defer func() {
		log.Printf("タイムライン (%s) に戻ります...", timelineURL)
		err := chromedp.Run(ctx,
			chromedp.Navigate(timelineURL),
			chromedp.WaitVisible(`.TimelineList__Feed`, chromedp.ByQuery),
		)
		if err != nil {
			log.Printf("タイムラインへの自動復帰に失敗しました: %v", err)
		}
	}()

	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible(`button[aria-label="絵文字をおくる"]`)); err != nil {
		return false, fmt.Errorf("投稿ページへの移動またはボタンの待機に失敗: %w", err)
	}

	var sendErr error
	for i := 0; i < 3; i++ {
		log.Printf("リアクション試行 %d回目: %s", i+1, url)
		sendErr = chromedp.Run(ctx,
			chromedp.Click(`button[aria-label="絵文字をおくる"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`.emojiPickerBody`),
			chromedp.Click(`button.emoji-picker-button[data-emoji-key='thumbs_up']`, chromedp.ByQuery),
			chromedp.Sleep(3*time.Second),
		)

		if sendErr == nil {
			log.Printf("リアクションの送信に成功しました: %s", url)
			return true, nil
		}
		log.Printf("試行 %d回目が失敗しました (%s): %v", i+1, url, sendErr)

		if i < 2 {
			log.Println("ページをリロードして再試行します...")
			if err := chromedp.Run(ctx, chromedp.Reload(), chromedp.WaitVisible(`button[aria-label="絵文字をおくる"]`)); err != nil {
				log.Printf("リロードに失敗: %v", err)
				return false, fmt.Errorf("リロード後のボタン待機に失敗: %w", err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	return false, fmt.Errorf("リアクションの送信に失敗しました（3回試行）: %w", sendErr)
}