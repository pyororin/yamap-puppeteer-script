package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

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
	log.Println("ヘッドレスブラウザを初期化しています...")
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/tmp/chrome/chrome"),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserDataDir("./user-data"),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-plugins-discovery", true),
		chromedp.Flag("disk-cache-dir", "null"),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
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

	// --- 最初のURLで絵文字ピッカーを開き、HTMLを取得 ---
	targetURL := urls[0]
	log.Printf("セレクタ特定のため、最初のURL (%s) で絵文字ピッカーを開きます...", targetURL)

	var htmlContent string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitVisible(`button[aria-label="絵文字をおくる"]`),
		logAction("「絵文字をおくる」ボタンをクリックします..."),
		chromedp.Click(`button[aria-label="絵文字をおくる"]`, chromedp.ByQuery),
		logAction("絵文字ピッカーの表示を待っています..."),
		chromedp.WaitVisible(`[data-testid="emoji-picker"]`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		logAction("HTMLを取得します..."),
		chromedp.OuterHTML("html", &htmlContent),
	); err != nil {
		log.Fatalf("絵文字ピッカーのHTML取得に失敗しました: %v", err)
	}

	log.Println("HTML取得成功。ファイル(debug_picker_output.html)に書き出します。")
	if err := ioutil.WriteFile("debug_picker_output.html", []byte(htmlContent), 0644); err != nil {
		log.Fatalf("HTMLのファイル書き出しに失敗しました: %v", err)
	}
	log.Println("処理を完了しました。次は取得したHTMLを分析し、リアクション機能を実装します。")
}

func login(ctx context.Context, email, password string) error {
	return chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/login"),
		chromedp.WaitVisible(`input[name="email"]`),
		logAction(`メールアドレスを入力します...`),
		chromedp.SendKeys(`input[name="email"]`, email),
		logAction(`パスワードを入力します...`),
		chromedp.SendKeys(`input[name="password"]`, password),
		logAction(`ログインボタンをクリックします...`),
		chromedp.Click(`button[data-testid="login-button"]`),
		logAction(`ログイン後のページ遷移を待っています...`),
		chromedp.WaitVisible(`a[href="/search/activities"]`),
	)
}

func fetchActivityURLs(ctx context.Context, count int) ([]string, error) {
	var urls []string
	var res string
	err := chromedp.Run(ctx,
		chromedp.Navigate("https://yamap.com/search/activities?sort=new"),
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

func logAction(message string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		log.Println(message)
		return nil
	})
}