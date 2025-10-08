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

	// --- URLリストの取得 ---
	log.Println("活動日記のURLリストを取得します...")
	urls, err := fetchActivityURLs(ctx, postCount)
	if err != nil {
		log.Fatalf("URLリストの取得に失敗しました: %v", err)
	}
	if len(urls) == 0 {
		log.Fatal("対象のURLが見つかりませんでした。")
	}
	log.Printf("%d件のURLを取得しました。", len(urls))

	// --- 各URLにリアクションを送信 ---
	for _, url := range urls {
		if err := sendReaction(ctx, url); err != nil {
			log.Printf("リアクション送信中にエラーが発生しました: %v", err)
			// 次のURLへ処理を続ける
		}
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
		logAction(`ログイン後のページ遷移を待っています...`),
		chromedp.WaitVisible(`h2.css-xp2hg4`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("ログイン後のページ遷移確認に失敗: %w", err)
	}

	log.Println("ログイン成功を確認しました。")
	return nil
}

func fetchActivityURLs(ctx context.Context, count int) ([]string, error) {
	var urls []string
	var res string
	err := chromedp.Run(ctx,
		chromedp.WaitVisible(`a[href^="/activities/"]`),
		chromedp.Evaluate(`
			(() => {
				const links = Array.from(document.querySelectorAll('a[href^="/activities/"]'));
				const hrefs = links.map(link => link.href);
				// 重複を除外して返す
				return JSON.stringify(Array.from(new Set(hrefs)));
			})()
		`, &res),
	)
	if err != nil {
		return nil, fmt.Errorf("URL取得のJavaScript実行に失敗: %w", err)
	}

	var allUrls []string
	if err := json.Unmarshal([]byte(res), &allUrls); err != nil {
		return nil, fmt.Errorf("URLのJSONパースに失敗: %w", err)
	}

	// landmarksを含まないURLをcountの数だけ収集
	for _, url := range allUrls {
		if len(urls) >= count {
			break
		}
		if !strings.Contains(url, "landmarks") {
			urls = append(urls, url)
		}
	}

	return urls, nil
}

func sendReaction(ctx context.Context, url string) error {
	log.Printf("リアクションを送信します: %s", url)
	var err error
	for i := 0; i < 3; i++ {
		log.Printf("Attempt %d for %s", i+1, url)
		err = chromedp.Run(ctx,
			chromedp.Navigate(url),
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
		if err == nil {
			log.Printf("リアクションの送信に成功しました: %s", url)
			return nil
		}
		log.Printf("Attempt %d failed for %s: %v", i+1, url, err)
		time.Sleep(2 * time.Second) // Wait before retrying
	}
	return fmt.Errorf("リアクションの送信に失敗しました（3回試行）: %s, error: %w", url, err)
}

func logAction(message string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		log.Println(message)
		return nil
	})
}