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

// --- 定数定義 ---
const (
	// URL
	loginURL    = "https://yamap.com/login"
	timelineURL = "https://yamap.com/timeline"
	activityURL = "https://yamap.com/activities/%d"

	// セレクタ
	emailInputSelector         = `input[name="email"]`
	passwordInputSelector      = `input[name="password"]`
	submitButtonSelector       = `button[type="submit"]`
	activityLinkSelector       = `a[href^="/activities/"]`
	reactionButtonSelector     = `button[aria-label="絵文字をおくる"]`
	emojiPickerBodySelector    = `.emojiPickerBody`
	thumbsUpButtonSelector     = `button.emoji-picker-button[data-emoji-key='thumbs_up']`
	scrollEvaluation           = `window.scrollTo(0, document.body.scrollHeight)`
	scrollHeightEvaluation     = `document.body.scrollHeight`
	timelineNuxtDataRegex      = `window\.__NUXT__\s*=\s*(.*?)\s*;\s*<\/script>`
	loginFailureScreenshotFile = "login_failure_screenshot.png"

	// タイムアウトと待機時間
	totalTimeout         = 15 * time.Minute
	loginTimeout         = 30 * time.Second
	reactionTimeout      = 2 * time.Minute
	postLoginWait        = 5 * time.Second
	reactionWait         = 3 * time.Second
	reloadWait           = 2 * time.Second
	navigationWait       = 1 * time.Second
	scrollWait           = 5 * time.Second
	scrollPostWait       = 3 * time.Second
	generalErrorWait     = 3 * time.Second
	maxReactionRetries   = 3
	maxNoNewContentTries = 3
)

// ActivityInfo holds the essential details for processing a post.
type ActivityInfo struct {
	URL string
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
		cu.WithTimeout(totalTimeout), // タイムライン処理全体を考慮してタイムアウトを延長
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
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(emailInputSelector),
		chromedp.SendKeys(emailInputSelector, email),
		chromedp.SendKeys(passwordInputSelector, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	// 2. ログインボタンをクリック
	log.Println("ログインボタンをクリックします...")
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf("document.querySelector('%s').click()", submitButtonSelector), nil),
	); err != nil {
		return fmt.Errorf("ログインボタンのクリックに失敗: %w", err)
	}

	// 3. ログイン後のページ遷移を待つ
	log.Println("ログイン後のタイムライン読み込みを待っています...")
	time.Sleep(postLoginWait)

	waitCtx, cancelWait := context.WithTimeout(ctx, loginTimeout)
	defer cancelWait()

	if err := chromedp.Run(waitCtx,
		chromedp.WaitVisible(activityLinkSelector, chromedp.ByQuery),
	); err != nil {
		// ログイン失敗時にスクリーンショットを撮る
		var buf []byte
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			log.Printf("スクリーンショットの取得に失敗: %v", err)
		} else if err := os.WriteFile(loginFailureScreenshotFile, buf, 0644); err != nil {
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
	successfulReactions := 0
	lastHeight := int64(0)
	noNewContentCount := 0

	// メインループ: 目標数のリアクションを送信するまで、または新しい投稿がなくなるまで続く
	for successfulReactions < postCountToProcess {
		// コンテキストがキャンセルされたか確認
		select {
		case <-ctx.Done():
			log.Println("処理がタイムアウトまたはキャンセルされました。")
			return ctx.Err()
		default:
		}
		// 現在のURLを確認し、タイムラインページでなければ移動する
		if err := ensureOnTimeline(ctx, timelineURL); err != nil {
			return err // タイムラインへの復帰に失敗した場合、致命的エラーとする
		}

		// タイムラインから未リアクションのアクティビティを抽出
		activities, err := extractActivitiesFromTimeline(ctx, seenActivityIDs)
		if err != nil {
			log.Printf("アクティビティの抽出に失敗しました: %v。スクロールして再試行します。", err)
			// スクロール処理へ
		} else if len(activities) > 0 {
			log.Printf("%d件の新しい未リアクション投稿を処理します。", len(activities))
			noNewContentCount = 0 // 新しい投稿が見つかったのでカウンターをリセット

			// 抽出したアクティビティにリアクションを送信
			for _, activity := range activities {
				if successfulReactions >= postCountToProcess {
					break // 目標数に達したら内部ループも抜ける
				}

				reactionCtx, cancel := context.WithTimeout(ctx, reactionTimeout)
				liked, reactionErr := sendReaction(reactionCtx, activity.URL)
				cancel()

				if reactionErr != nil {
					log.Printf("リアクション処理でエラーが発生しました (%s): %v", activity.URL, reactionErr)
					// エラーが発生しても処理を続行するが、タイムラインへの復帰はここで行う
				} else if liked {
					successfulReactions++
					log.Printf("いいね！しました。(現在 %d/%d 件)", successfulReactions, postCountToProcess)
				}

				// 次の投稿処理の前に、タイムラインページにいることを確認
				if err := ensureOnTimeline(ctx, timelineURL); err != nil {
					log.Printf("リアクション後のタイムライン復帰に失敗: %v", err)
					return err // 致命的エラー
				}
				time.Sleep(navigationWait) // サーバーへの負荷を軽減
			}
		} else {
			noNewContentCount++
			log.Printf("新しい未リアクションの投稿が見つかりませんでした。(試行 %d/%d)", noNewContentCount, maxNoNewContentTries)
		}

		// ループの終了条件を確認
		if successfulReactions >= postCountToProcess {
			log.Printf("目標の %d 件の「いいね！」を達成しました。", postCountToProcess)
			break
		}

		// ページをスクロールして新しいコンテンツを読み込む
		scrolled, newHeight, err := scrollTimeline(ctx, lastHeight)
		if err != nil {
			log.Printf("ページのスクロールに失敗: %v", err)
			break // スクロールに失敗したら終了
		}
		lastHeight = newHeight

		// スクロールしても新しいコンテンツが読み込まれなかった場合の終了判定
		if !scrolled && noNewContentCount >= maxNoNewContentTries {
			log.Println("タイムラインの終端に到達したか、新しい投稿が読み込まれませんでした。処理を終了します。")
			break
		}

		// 新しいコンテンツが表示されるのを待つ
		time.Sleep(scrollWait)
	}

	log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", successfulReactions)
	return nil
}

// ensureOnTimeline は、ブラウザが現在タイムラインページにあることを確認し、そうでなければ指定されたURLに移動します。
func ensureOnTimeline(ctx context.Context, timelineURL string) error {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return fmt.Errorf("現在のURLの取得に失敗: %w", err)
	}

	if !strings.HasPrefix(currentURL, timelineURL) {
		log.Printf("現在地がタイムラインではありません (%s)。移動します...", currentURL)
		if err := chromedp.Run(ctx, chromedp.Navigate(timelineURL), chromedp.WaitReady("body")); err != nil {
			return fmt.Errorf("タイムラインへの移動に失敗 (%s): %w", timelineURL, err)
		}
		log.Println("タイムラインに正常に移動しました。")
	}
	return nil
}

// extractActivitiesFromTimeline は、タイムラインのHTMLから__NUXT__データを抽出し、
// まだ処理されていない（seenActivityIDsにない）未リアクションのアクティビティリストを返します。
func extractActivitiesFromTimeline(ctx context.Context, seenActivityIDs map[int64]struct{}) ([]ActivityInfo, error) {
	var htmlContent string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &htmlContent)); err != nil {
		return nil, fmt.Errorf("タイムラインのHTML取得に失敗: %w", err)
	}

	re := regexp.MustCompile(timelineNuxtDataRegex)
	matches := re.FindStringSubmatch(htmlContent)
	if len(matches) < 2 {
		return nil, fmt.Errorf("NUXTデータがHTML内に見つかりません")
	}

	var nuxtData NuxtTimelineData
	if err := json.Unmarshal([]byte(matches[1]), &nuxtData); err != nil {
		return nil, fmt.Errorf("NUXTデータのJSONパースに失敗: %w", err)
	}

	var newActivities []ActivityInfo
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
					url := fmt.Sprintf(activityURL, activity.ID)
					newActivities = append(newActivities, ActivityInfo{URL: url})
					log.Printf("新しい未リアクション投稿を発見: %s", url)
				}
			}
		}
	}
	return newActivities, nil
}

// scrollTimeline は、ページを指定された高さまでスクロールし、新しい高さを返します。
// スクロールが成功したか（ページの高さが変わったか）どうかのフラグも返します。
func scrollTimeline(ctx context.Context, lastHeight int64) (bool, int64, error) {
	log.Println("ページを下にスクロールして新しい投稿を読み込みます...")
	if err := chromedp.Run(ctx, chromedp.Evaluate(scrollEvaluation, nil)); err != nil {
		return false, lastHeight, fmt.Errorf("ページスクロールのJavaScript実行に失敗: %w", err)
	}

	// スクロール後に新しいコンテンツが読み込まれるのを少し待つ
	time.Sleep(scrollPostWait)

	var newHeight int64
	if err := chromedp.Run(ctx, chromedp.Evaluate(scrollHeightEvaluation, &newHeight)); err != nil {
		return false, lastHeight, fmt.Errorf("ページの新しい高さの取得に失敗: %w", err)
	}

	return newHeight > lastHeight, newHeight, nil
}

func sendReaction(ctx context.Context, url string) (bool, error) {
	log.Printf("投稿ページに移動してリアクションを送信します: %s", url)
	// 投稿ページに移動
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.WaitVisible(reactionButtonSelector)); err != nil {
		return false, fmt.Errorf("投稿ページへの移動またはボタンの待機に失敗: %w", err)
	}

	// リアクションボタンのクリックを3回試行
	var sendErr error
	for i := 0; i < maxReactionRetries; i++ {
		log.Printf("リアクション試行 %d回目: %s", i+1, url)
		sendErr = chromedp.Run(ctx,
			chromedp.Click(reactionButtonSelector, chromedp.ByQuery),
			chromedp.WaitVisible(emojiPickerBodySelector),
			chromedp.Click(thumbsUpButtonSelector, chromedp.ByQuery),
			chromedp.Sleep(reactionWait), // リアクションが処理されるのを待つ
		)

		if sendErr == nil {
			log.Printf("リアクションの送信に成功しました: %s", url)
			return true, nil
		}
		log.Printf("試行 %d回目が失敗しました (%s): %v", i+1, url, sendErr)

		if i < maxReactionRetries-1 {
			log.Println("ページをリロードして再試行します...")
			if err := chromedp.Run(ctx, chromedp.Reload(), chromedp.WaitVisible(reactionButtonSelector)); err != nil {
				log.Printf("リロードに失敗: %v", err)
				return false, fmt.Errorf("リロード後のボタン待機に失敗: %w", err)
			}
			time.Sleep(reloadWait)
		}
	}

	return false, fmt.Errorf("リアクションの送信に失敗しました（%d回試行）: %w", maxReactionRetries, sendErr)
}