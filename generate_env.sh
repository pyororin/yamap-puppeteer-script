#!/bin/bash

# 環境変数から値を読み取り、.envファイルを作成/上書きするスクリプト

# .envファイルを初期化（上書き）
echo "YAMAP_EMAIL=${YAMAP_EMAIL}" > .env
echo "ACTIVITIES_POST_COUNT_TO_PROCESS=${ACTIVITIES_POST_COUNT_TO_PROCESS}" >> .env
echo "YAMAP_PASSWORD=${YAMAP_PASSWORD}" >> .env
echo "TIMELINE_POST_COUNT_TO_PROCESS=${TIMELINE_POST_COUNT_TO_PROCESS}" >> .env

echo ".env file has been generated."
