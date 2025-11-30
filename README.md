# YAMAP Timeline Crawler in Go

This script uses Go and `chromedp` to automatically scroll through the YAMAP timeline and react to posts.

## Features

- Logs into YAMAP automatically.
- Scrolls through the timeline to load a specified number of posts.
- Automatically sends a reaction to posts that have not been reacted to yet.
- Includes a debug mode to save the current page's HTML for easier selector analysis.

## Requirements

- Go 1.18 or higher

## Setup

1.  **Install Go libraries:**
    ```bash
    go get github.com/chromedp/chromedp
    go get github.com/joho/godotenv
    ```

2.  **Environment Setup:**
    Ensure that a `.env` file exists in the root directory. This file must contain your YAMAP credentials.

### Normal Execution

First, build the main program:
```bash
go build -o yamap-crawler index.go
```

Then, run the compiled program:
```bash
./yamap-crawler
```

### Debug Mode

First, build the debug program:
```bash
go build -o debug debug.go
```
Then, run it:
```bash
./debug
```
This will save the current HTML of the page to `debug_output.html`.