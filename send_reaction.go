package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	cu "github.com/Davincible/chromedp-undetected"
	"github.com/chromedp/chromedp"
)

// ActivityInfo holds the essential details for processing a post.
type ActivityInfo struct {
	URL     string
	Reacted bool
}

// NuxtTimelineData is the structure for the relevant parts of the window.__NUXT__ object on the timeline page.
// It is designed to extract a list of activities and their reaction status.
type NuxtTimelineData struct {
	State struct {
		Feed struct { // Assuming 'feed' is the key for the timeline data
			Items []struct {
				Activity struct {
					ID             int64 `json:"id"`
					EmojiReactions []struct {
						ViewerHasReacted bool `json:"viewer_has_reacted"`
					} `json:"emoji_reactions"`
				} `json:"activity"`
			} `json:"items"`
		} `json:"feed"`
	} `json:"state"`
}

func main() {
	// --- Chromedpの初期化 ---
	log.Println("undetected-chromedpを使用してヘッドレスブラウザを初期化しています...")
	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithHeadless(),
		cu.WithTimeout(15*time.Minute), // タイムライン処理全体を考慮してタイムアウトを延長
		cu.WithChromeFlags(chromedp.ExecPath("/home/jules/.cache/ms-playwright/chromium-1181/chrome-linux/chrome")),
	))
	if err != nil {
		log.Fatalf("undetected-chromedpの初期化に失敗しました: %v", err)
	}
	defer cancel()

	// --- 環境変数の読み込み ---
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

	// --- ログイン処理 ---
	log.Println("ログイン処理を開始します...")
	if err := login(ctx, email, password); err != nil {
		log.Fatalf("ログインに失敗しました: %v", err)
	}
	log.Println("ログインに成功しました。")

	// --- タイムライン処理 ---
	log.Println("タイムラインの処理を開始します...")
	if err := processTimeline(ctx, postCount); err != nil {
		log.Fatalf("タイムライン処理中に致命的なエラーが発生しました: %v", err)
	}

	log.Println("すべての処理が完了しました。")
}

func login(ctx context.Context, email, password string) error {
	// 1. ページに移動し、フォームを入力
	log.Println("ログインページに移動し、フォームを入力します...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		chromedp.SendKeys(`input[name="email"]`, email),
		chromedp.SendKeys(`input[name="password"]`, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	// 2. ログインボタンをクリック
	log.Println("ログインボタンをクリックします...")
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('button[type="submit"]').click()`, nil),
	); err != nil {
		return fmt.Errorf("ログインボタンのクリックに失敗: %w", err)
	}

	// 3. ログイン後のページ遷移を待つ
	log.Println("ログイン後のタイムライン読み込みを待っています...")
	time.Sleep(5 * time.Second)

	waitCtx, cancelWait := context.WithTimeout(ctx, 30*time.Second)
	defer cancelWait()

	if err := chromedp.Run(waitCtx,
		chromedp.WaitVisible(`a[href^="/activities/"]`, chromedp.ByQuery),
	); err != nil {
		// ログイン失敗時にスクリーンショットを撮る
		var buf []byte
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			log.Printf("スクリーンショットの取得に失敗: %v", err)
		} else if err := os.WriteFile("login_failure_screenshot.png", buf, 0644); err != nil {
			log.Printf("スクリーンショットの保存に失敗: %v", err)
		}
		return fmt.Errorf("ログイン後のタイムライン読み込み確認に失敗: %w", err)
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
		var activitiesToProcess []ActivityInfo // ループの先頭で宣言
		var nuxtData NuxtTimelineData             // gotoエラーを避けるためここで宣言
		var jsonData string                      // gotoエラーを避けるためここで宣言
		select {
		case <-ctx.Done():
			log.Println("処理中にタイムアウトしました。")
			return ctx.Err()
		default:
		}

		// 毎回タイムラインページにいることを確認・復帰
		var currentURL string
		chromedp.Run(ctx, chromedp.Location(&currentURL))
		if !strings.HasPrefix(currentURL, timelineURL) {
			log.Printf("タイムラインページ (%s) にいません。移動します...", timelineURL)
			if err := chromedp.Run(ctx, chromedp.Navigate(timelineURL), chromedp.WaitReady("body")); err != nil {
				log.Printf("タイムラインへの復帰に失敗: %v", err)
				return err // 致命的エラー
			}
		}

		// 1. タイムラインのHTMLから__NUXT__データを取得
		var htmlContent string
		if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &htmlContent)); err != nil {
			log.Printf("タイムラインのHTML取得に失敗: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}

		re := regexp.MustCompile(`window\.__NUXT__\s*=\s*(.*?)\s*;\s*<\/script>`)
		matches := re.FindStringSubmatch(htmlContent)
		if len(matches) < 2 {
			log.Println("タイムラインページからNUXTデータが見つかりませんでした。スクロールして再試行します。")
			goto scroll
		}
		jsonData = matches[1]

		if err := json.Unmarshal([]byte(jsonData), &nuxtData); err != nil {
			log.Printf("NUXTデータのJSONパースに失敗: %v。スクロールして再試行します。", err)
			goto scroll
		}

		// 2. __NUXT__データから未リアクションのアクティビティを抽出
		activitiesToProcess = nil // ループの各イテレーションでスライスをクリア
		if len(nuxtData.State.Feed.Items) > 0 {
			for _, item := range nuxtData.State.Feed.Items {
				activity := item.Activity
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

		// 3. 未リアクションの投稿にリアクションを送信
		for _, activity := range activitiesToProcess {
			reactionCtx, reactionCancel := context.WithTimeout(ctx, 2*time.Minute)
			liked, err := sendReaction(reactionCtx, activity.URL, timelineURL)
			reactionCancel()

			if err != nil {
				log.Printf("リアクション処理でエラーが発生しました (%s): %v", activity.URL, err)
				// sendReactionがタイムラインへの復帰を試みるので、ここではループを続ける
			}
			if liked {
				successfulReactions++
				log.Printf("いいね！しました。(現在 %d/%d 件)", successfulReactions, postCountToProcess)
				if successfulReactions >= postCountToProcess {
					log.Printf("目標の %d 件の「いいね！」を達成しました。", postCountToProcess)
					goto end
				}
			}
			// sendReactionから戻ってきたら、次の投稿処理に移る前に少し待つ
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
		// エラーが発生しても、タイムラインへの復帰を試みる
		err := chromedp.Run(ctx,
			chromedp.Navigate(timelineURL),
			chromedp.WaitReady("body"),
		)
		if err != nil {
			log.Printf("タイムラインへの自動復帰に失敗しました: %v", err)
		}
	}()

	// 投稿ページに移動
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible(`button[aria-label="絵文字をおくる"]`)); err != nil {
		return false, fmt.Errorf("投稿ページへの移動またはボタンの待機に失敗: %w", err)
	}

	// リアクションボタンのクリックを3回試行
	var sendErr error
	for i := 0; i < 3; i++ {
		log.Printf("リアクション試行 %d回目: %s", i+1, url)
		sendErr = chromedp.Run(ctx,
			chromedp.Click(`button[aria-label="絵文字をおくる"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`.emojiPickerBody`),
			chromedp.Click(`button.emoji-picker-button[data-emoji-key='thumbs_up']`, chromedp.ByQuery),
			chromedp.Sleep(3*time.Second), // リアクションが処理されるのを待つ
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
