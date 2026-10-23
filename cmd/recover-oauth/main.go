package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const overview = `OAuth 帳號復原工具

網站擁有者可在透過可信管道核實使用者身分後，協助符合資格的舊 OAuth
帳號恢復存取。復原授權仍要求使用者透過原提供者完成 OAuth 驗證。

用法：
  recover-oauth <list|issue> [選項]
  recover-oauth --help

命令：
  list   列出符合條件的舊 OAuth 帳號。
  issue  為符合條件的帳號簽發復原授權。

每個命令都必須提供 --site-dir <dir>，指定既有網站目錄。
非空的 JWT_SECRET 環境變數會覆寫既有的
<site-dir>/data/auth-secrets.json JWT 金鑰。JWT_SECRET 未設定或為空時，
使用既有檔案。該檔案唯讀；工具不會建立或替換金鑰、安裝網站、遷移資料庫，
也不會啟動伺服器。

操作成功時，JSON 物件只輸出到 stdout；錯誤輸出到 stderr。
參數錯誤以 2 結束，操作失敗以 1 結束，顯示說明以 0 結束。

範例：
  recover-oauth list --site-dir /srv/jelly
  recover-oauth list --site-dir /srv/jelly --limit 25 --after-id 100
  recover-oauth issue --site-dir /srv/jelly --user-id 42 --provider google
  JWT_SECRET='replacement-secret' recover-oauth issue --site-dir /srv/jelly --user-id 42 --provider github

命令細節請查看「recover-oauth list --help」或「recover-oauth issue --help」。
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && isHelp(args[0])) {
		_, _ = io.WriteString(stdout, overview)
		return 0
	}

	command := args[0]
	commandArgs := args[1:]
	switch command {
	case "list":
		return runList(commandArgs, stdout, stderr)
	case "issue":
		return runIssue(commandArgs, stdout, stderr)
	default:
		return invalid(stderr, fmt.Sprintf("unknown command %q", command))
	}
}

func runList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	siteDir := fs.String("site-dir", "", "既有網站目錄（必填）")
	limit := fs.Int("limit", 50, "列出筆數上限（1 至 500）")
	afterIDText := fs.String("after-id", "0", "只列出 ID 大於此值的帳號")
	help := fs.Bool("help", false, "顯示 list 說明")
	if err := fs.Parse(args); err != nil {
		return commandError(stderr, err, listHelp)
	}
	if *help {
		if fs.NArg() != 0 {
			return commandError(stderr, fmt.Errorf("unexpected positional arguments"), listHelp)
		}
		_, _ = io.WriteString(stdout, listHelp)
		return 0
	}
	if fs.NArg() != 0 {
		return commandError(stderr, fmt.Errorf("unexpected positional argument %q", fs.Arg(0)), listHelp)
	}
	if strings.TrimSpace(*siteDir) == "" {
		return commandError(stderr, fmt.Errorf("--site-dir is required and must not be blank"), listHelp)
	}
	if *limit < 1 || *limit > 500 {
		return commandError(stderr, fmt.Errorf("--limit must be between 1 and 500"), listHelp)
	}
	afterID, err := strconv.ParseUint(*afterIDText, 10, strconv.IntSize)
	if err != nil {
		return commandError(stderr, fmt.Errorf("--after-id must be a non-negative uint: %w", err), listHelp)
	}
	response := listResponse{Users: make([]recoveryAccount, 0)}
	err = withSiteDB(*siteDir, func(db *gorm.DB) error {
		rows := make([]recoveryAccount, 0, *limit+1)
		if err := db.WithContext(context.Background()).Raw(`
			SELECT u.id, u.username, u.email, u.provider
			FROM users AS u
			WHERE `+eligibleOAuthAccountPredicate+`
			  AND u.id > ?
			ORDER BY u.id ASC
			LIMIT ?`, uint(afterID), *limit+1).Scan(&rows).Error; err != nil {
			return err
		}
		if rows == nil {
			rows = make([]recoveryAccount, 0)
		}
		if len(rows) > *limit {
			cursor := rows[*limit-1].ID
			response.NextAfterID = &cursor
			rows = rows[:*limit]
		}
		response.Users = rows
		return nil
	})
	if err != nil {
		return operationalFailure(stderr)
	}
	return writeJSON(stdout, stderr, response)
}

func runIssue(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	siteDir := fs.String("site-dir", "", "既有網站目錄（必填）")
	userIDText := fs.String("user-id", "", "正整數帳號 ID（必填）")
	provider := fs.String("provider", "", "原始 OAuth 提供者：google 或 github（必填）")
	help := fs.Bool("help", false, "顯示 issue 說明")
	if err := fs.Parse(args); err != nil {
		return commandError(stderr, err, issueHelp)
	}
	if *help {
		if fs.NArg() != 0 {
			return commandError(stderr, fmt.Errorf("unexpected positional arguments"), issueHelp)
		}
		_, _ = io.WriteString(stdout, issueHelp)
		return 0
	}
	if fs.NArg() != 0 {
		return commandError(stderr, fmt.Errorf("unexpected positional argument %q", fs.Arg(0)), issueHelp)
	}
	if strings.TrimSpace(*siteDir) == "" {
		return commandError(stderr, fmt.Errorf("--site-dir is required and must not be blank"), issueHelp)
	}
	userID, err := strconv.ParseUint(*userIDText, 10, strconv.IntSize)
	if err != nil || userID == 0 {
		return commandError(stderr, fmt.Errorf("--user-id must be a positive uint"), issueHelp)
	}
	if *provider != "google" && *provider != "github" {
		return commandError(stderr, fmt.Errorf("--provider must be google or github"), issueHelp)
	}
	var response issueResponse
	err = withSiteDB(*siteDir, func(db *gorm.DB) error {
		ctx := context.Background()
		var account recoveryAccount
		row := db.WithContext(ctx).Raw(`
			SELECT u.id, u.username, u.email, u.provider
			FROM users AS u
			WHERE u.id = ? AND u.provider = ?
			  AND `+eligibleOAuthAccountPredicate+`
			LIMIT 1`, uint(userID), *provider).Row()
		if err := row.Scan(&account.ID, &account.Username, &account.Email, &account.Provider); err != nil {
			return err
		}
		grant, expiresAt, err := auth.IssueOAuthRecoveryGrant(ctx, uint(userID), *provider)
		if err != nil {
			return err
		}
		response = issueResponse{
			UserID:    account.ID,
			Username:  account.Username,
			Email:     account.Email,
			Provider:  account.Provider,
			ExpiresAt: expiresAt.Format(time.RFC3339),
			Grant:     grant,
		}
		return nil
	})
	if err != nil {
		return operationalFailure(stderr)
	}
	return writeJSON(stdout, stderr, response)
}

type recoveryAccount struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
}

type listResponse struct {
	Users       []recoveryAccount `json:"users"`
	NextAfterID *uint             `json:"next_after_id"`
}

type issueResponse struct {
	UserID    uint   `json:"user_id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Provider  string `json:"provider"`
	ExpiresAt string `json:"expires_at"`
	Grant     string `json:"grant"`
}

const eligibleOAuthAccountPredicate = `u.provider IN ('google', 'github')
	AND NOT (u.provider = 'local' AND COALESCE(u.password, '') <> '')
	AND NOT EXISTS (
		SELECT 1 FROM oauth_identities AS identity
		WHERE identity.user_id = u.id AND identity.deleted_at IS NULL
	)`

func withSiteDB(siteDir string, operation func(*gorm.DB) error) (resultErr error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	absSiteDir, err := filepath.Abs(siteDir)
	if err != nil {
		return err
	}
	if err := os.Chdir(absSiteDir); err != nil {
		return err
	}
	defer func() {
		if err := os.Chdir(workingDirectory); resultErr == nil && err != nil {
			resultErr = err
		}
	}()

	previousConfig := config.GlobalConfig
	defer func() { config.GlobalConfig = previousConfig }()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := auth.InitOAuthRecovery(); err != nil {
		return err
	}
	var db *gorm.DB
	func() {
		previousLogger := logger.Default
		logger.Default = previousLogger.LogMode(logger.Silent)
		defer func() { logger.Default = previousLogger }()
		db, err = data.Open(cfg.Database)
	}()
	if err != nil {
		return err
	}
	db.Logger = db.Logger.LogMode(logger.Silent)
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	previousDB := data.DB
	data.DB = db
	defer func() { data.DB = previousDB }()
	return operation(db)
}

func operationalFailure(stderr io.Writer) int {
	_, _ = fmt.Fprintln(stderr, "OAuth recovery operation failed")
	return 1
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return operationalFailure(stderr)
	}
	encoded = append(encoded, '\n')
	if _, err := stdout.Write(encoded); err != nil {
		return operationalFailure(stderr)
	}
	return 0
}

func commandError(stderr io.Writer, err error, usage string) int {
	_, _ = fmt.Fprintf(stderr, "Error: %v\n\n%s", err, usage)
	return 2
}

func invalid(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "Error: %s\n\n%s", message, overview)
	return 2
}

func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h"
}

const listHelp = overview + `
列出符合條件的舊 OAuth 帳號

用法：
  recover-oauth list --site-dir <dir> [--limit 50] [--after-id 0]

必要參數：
  --site-dir <dir>  既有網站目錄。

選項：
  --limit <n>       最多列出幾筆，範圍為 1 至 500，預設 50。
  --after-id <id>   只列出 ID 大於此值的帳號，預設 0。
  --help            顯示本命令說明。

成功時 stdout 輸出一個 JSON 物件。帳號依 ID 遞增排列，最多額外讀取一筆，
用來判斷是否還有下一頁：
  {"users":[{"id":12,"username":"legacy-user","email":"user@example.com","provider":"google"}],"next_after_id":12}
  {"users":[],"next_after_id":null}

參數錯誤以 2 結束，並在 stderr 顯示用法。設定、資料庫、金鑰或帳號資格錯誤
以 1 結束，並在 stderr 顯示一般錯誤。

範例：
  recover-oauth list --site-dir /srv/jelly
  recover-oauth list --site-dir /srv/jelly --limit 25 --after-id 100
`

const issueHelp = overview + `
為符合條件的舊 OAuth 帳號簽發復原授權

用法：
  recover-oauth issue --site-dir <dir> --user-id <id> --provider <google|github>

必要參數：
  --site-dir <dir>       既有網站目錄。
  --user-id <id>         可由目前平台 uint 表示的正整數帳號 ID。
  --provider <provider>  原始 OAuth 提供者，只能是 google 或 github。

選項：
  --help                 顯示本命令說明。

成功時 stdout 輸出一個 JSON 物件。expires_at 使用 RFC3339 格式，grant 只顯示一次；
請透過可信管道將授權交給已核實身分的帳號本人：
  {"user_id":42,"username":"legacy-user","email":"user@example.com","provider":"google","expires_at":"2030-01-02T03:04:05Z","grant":"<short-lived-recovery-grant>"}

參數錯誤以 2 結束，並在 stderr 顯示用法。設定、資料庫、金鑰或帳號資格錯誤
以 1 結束，並在 stderr 顯示一般錯誤。

非空的 JWT_SECRET 環境變數會覆寫既有的
<site-dir>/data/auth-secrets.json JWT 金鑰。JWT_SECRET 未設定或為空時，
使用既有檔案，不會修改、建立或替換金鑰。

範例：
  recover-oauth issue --site-dir /srv/jelly --user-id 42 --provider google
  JWT_SECRET='replacement-secret' recover-oauth issue --site-dir /srv/jelly --user-id 42 --provider github
`
