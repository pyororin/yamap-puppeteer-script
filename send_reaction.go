package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	cu "github.com/Davincible/chromedp-undetected"
	"github.com/chromedp/chromedp"
)

// .envファイルを読み込み、環境変数を設定する
func loadEnv(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return nil // .envがなくてもエラーにしない
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if len(value) > 1 && (value[0] == '"' && value[len(value)-1] == '"') {
			value = value[1 : len(value)-1]
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}

func main() {
	if err := loadEnv(".env"); err != nil {
		log.Fatalf(".envファイルの読み込みに失敗しました: %v", err)
	}

	// --- Chromedpの初期化 ---
	log.Println("undetected-chromedpを使用してヘッドレスブラウザを初期化しています...")
	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithHeadless(),
		cu.WithTimeout(4*time.Minute),
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
		log.Fatal(".envファイルにYAMAP_EMAIL, YAMAP_PASSWORD, POST_COUNT_TO_PROCESSを設定してください。")
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
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		logAction(`メールアドレスを入力します...`),
		chromedp.SendKeys(`input[name="email"]`, email),
		logAction(`パスワードを入力します...`),
		chromedp.SendKeys(`input[name="password"]`, password),
	); err != nil {
		return fmt.Errorf("フォーム入力に失敗: %w", err)
	}

	// 2. ログインボタンをクリック
	logAction(`ログインボタンをクリックします...`)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('button[type="submit"]').click()`, nil),
	); err != nil {
		return fmt.Errorf("ログインボタンのクリックに失敗: %w", err)
	}

	// 3. ログイン後のページ遷移を待つ
	logAction("ログインボタンクリック後、5秒待機します...")
	time.Sleep(5 * time.Second)

	waitCtx, cancelWait := context.WithTimeout(ctx, 30*time.Second)
	defer cancelWait()

	if err := chromedp.Run(waitCtx,
		logAction(`タイムラインの読み込みを待っています...`),
		chromedp.WaitVisible(`a[href^="/activities/"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("ログイン後のタイムライン読み込み確認に失敗: %w", err)
	}

	log.Println("ログイン成功を確認しました。")
	return nil
}

func processTimeline(ctx context.Context, postCountToProcess int) error {
	log.Println("タイムラインのURL収集とリアクション送信を開始します。")

	seenUrls := make(map[string]struct{})
	var successfulReactions int
	var lastHeight int64
	noNewUrlsCount := 0

	// 15分間の全体的なタイムアウト
	procCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	for successfulReactions < postCountToProcess {
		select {
		case <-procCtx.Done():
			log.Println("処理中にタイムアウトしました。")
			// タイムアウトはエラーではなく、現在の状態で終了
			log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", successfulReactions)
			return nil
		default:
		}

		// 現在のURLをすべて取得
		var res string
		err := chromedp.Run(procCtx,
			chromedp.Evaluate(`
                (() => {
                    const links = Array.from(document.querySelectorAll('a[href^="/activities/"]'));
                    const hrefs = links.map(link => link.href);
                    return JSON.stringify(Array.from(new Set(hrefs)));
                })()
            `, &res),
		)
		if err != nil {
			log.Printf("URL取得のJavaScript実行に失敗: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var currentUrls []string
		if err := json.Unmarshal([]byte(res), &currentUrls); err != nil {
			log.Printf("URLのJSONパースに失敗: %v", err)
			continue
		}

		newUrlsFound := false
		for _, url := range currentUrls {
			if _, seen := seenUrls[url]; !seen {
				seenUrls[url] = struct{}{}
				newUrlsFound = true

				if !strings.Contains(url, "landmarks") && !strings.HasSuffix(url, "/new") {
					log.Printf("新しい投稿を処理します: %s", url)
					// ページ遷移を伴うため、全体のコンテキストとは別のコンテキストで実行
					reactionCtx, reactionCancel := context.WithTimeout(procCtx, 1*time.Minute)
					liked, err := sendReaction(reactionCtx, url)
					reactionCancel()

					if err != nil {
						log.Printf("リアクション処理中にエラーが発生しました (%s): %v", url, err)
						// エラーが発生した投稿はスキップして次に進む
						continue
					}
					if liked {
						successfulReactions++
						log.Printf("いいね！しました。(現在 %d/%d 件)", successfulReactions, postCountToProcess)
						if successfulReactions >= postCountToProcess {
							break // 目標数に達したら内部ループを抜ける
						}
					}
				}
			}
		}

		if successfulReactions >= postCountToProcess {
			log.Printf("目標の %d 件の「いいね！」を達成しました。", postCountToProcess)
			break // メインループを抜ける
		}

		// スクロール処理
		var currentHeight int64
		if err := chromedp.Run(procCtx, chromedp.Evaluate(`document.body.scrollHeight`, &currentHeight)); err != nil {
			log.Printf("ページの高さの取得に失敗: %v", err)
			break
		}

		if !newUrlsFound && currentHeight == lastHeight {
			noNewUrlsCount++
			log.Printf("新しいURLが見つかりませんでした。(試行 %d/3)", noNewUrlsCount)
			if noNewUrlsCount >= 3 {
				log.Println("タイムラインの終端に到達したか、新しい投稿が読み込まれませんでした。処理を終了します。")
				break
			}
		} else {
			noNewUrlsCount = 0 // 新しいURLが見つかればリセット
		}
		lastHeight = currentHeight

		log.Println("ページを下にスクロールします...")
		if err := chromedp.Run(procCtx, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil)); err != nil {
			log.Printf("ページスクロールに失敗: %v", err)
			break
		}
		time.Sleep(5 * time.Second) // 新しいコンテンツが読み込まれるのを待つ
	}

	log.Printf("いいね！の送信が完了しました。最終的な成功件数: %d", successfulReactions)
	return nil
}

func sendReaction(ctx context.Context, url string) (bool, error) {
	log.Printf("投稿ページを確認中: %s", url)

	// 1. ページに移動
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"), // bodyが読み込まれるのを待つ
	); err != nil {
		return false, fmt.Errorf("ページへの移動に失敗 (%s): %w", url, err)
	}

	// 2. すでにリアクション済みか確認
	var isReacted bool
	// "あなたのリアクション" というaria-labelを持つボタンが存在するかどうかで判定
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector("button[aria-label^='あなたのリアクション']") !== null`, &isReacted),
	); err != nil {
		log.Printf("リアクション済みかの確認に失敗: %v。リアクションを試みます。", err)
	}

	if isReacted {
		log.Printf("この投稿はすでにリアクション済みです: %s", url)
		return false, nil // リアクションせず、正常終了
	}

	log.Printf("リアクションを送信します: %s", url)
	// 3. リアクションを送信（リトライロジック）
	var sendErr error
	for i := 0; i < 3; i++ {
		log.Printf("Attempt %d for %s", i+1, url)
		sendErr = chromedp.Run(ctx,
			chromedp.WaitVisible(`button[aria-label="絵文字をおくる"]`),
			logAction("「絵文字をおくる」ボタンをクリックします..."),
			chromedp.Click(`button[aria-label="絵文字をおくる"]`, chromedp.ByQuery),
			logAction("絵文字ピッカーが表示されるのを待ちます..."),
			chromedp.WaitVisible(`.emojiPickerBody`),
			logAction("「thumbs_up」絵文字をクリックします..."),
			chromedp.Click(`button.emoji-picker-button[data-emoji-key='thumbs_up']`, chromedp.ByQuery),
			logAction("リアクション送信後、3秒待機します..."),
			chromedp.Sleep(3*time.Second),
		)
		if sendErr == nil {
			log.Printf("リアクションの送信に成功しました: %s", url)
			return true, nil // リアクション成功
		}
		log.Printf("Attempt %d failed for %s: %v", i+1, url, sendErr)
		time.Sleep(2 * time.Second) // Wait before retrying
	}
	return false, fmt.Errorf("リアクションの送信に失敗しました（3回試行）: %s, error: %w", url, sendErr)
}

func logAction(message string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		log.Println(message)
		return nil
	})
}