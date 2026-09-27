package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/config"
	"ai-router-gateway/internal/db"
	"ai-router-gateway/internal/handlers"
	"ai-router-gateway/internal/logger"
	"ai-router-gateway/internal/price"
	"ai-router-gateway/internal/proxy"
	"ai-router-gateway/internal/router"
	"ai-router-gateway/internal/task"

	"github.com/gin-gonic/gin"
)

const maxRequestBodyBytes = 10 << 20 // 10 MB

// resolveAPIKeyUser 通过 API Key 的 SHA-256 哈希查找所属用户，并带回其
// username / role（用于鉴权上下文）。注意：username / role 属于 users 表，
// 必须通过 JOIN 获取，api_keys 表本身没有这两列（早期版本误写导致鉴权静默失效）。
// 找不到或密钥被禁用时 ok=false。
func resolveAPIKeyUser(db *sql.DB, keyHash string) (userID int64, username, role string, ok bool) {
	err := db.QueryRow(
		"SELECT k.user_id, u.username, u.role FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ? AND k.enabled = 1",
		keyHash,
	).Scan(&userID, &username, &role)
	if err != nil {
		return 0, "", "", false
	}
	return userID, username, role, true
}

func combinedAuth(database *sql.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录请求到达时间，供四段式追踪的 auth 段计算鉴权耗时。
		c.Set("req_start", time.Now())
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token := parts[1]

				if strings.HasPrefix(token, "ark_") {
					h := sha256.Sum256([]byte(token))
					if userID, username, role, found := resolveAPIKeyUser(database, hex.EncodeToString(h[:])); found {
						c.Set("userID", userID)
						c.Set("username", username)
						c.Set("role", role)
						c.Next()
						return
					}
				}

				claims, err := auth.ValidateToken(token, jwtSecret)
				if err == nil {
					c.Set("userID", claims.UserID)
					c.Set("username", claims.Username)
					c.Set("role", claims.Role)
					c.Next()
					return
				}
			}
		}

		rawKey := c.GetHeader("x-api-key")
		if rawKey != "" {
			h := sha256.Sum256([]byte(rawKey))
			if userID, username, role, found := resolveAPIKeyUser(database, hex.EncodeToString(h[:])); found {
				c.Set("userID", userID)
				c.Set("username", username)
				c.Set("role", role)
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "授权信息无效或缺失"})
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func main() {
	logger.Init("info")
	l := logger.FromContext(nil)

	cfg := config.Load()

	database, err := db.InitDB(cfg.DBType, cfg.DBPath)
	if err != nil {
		logger.Fatalf("初始化数据库失败：%v", err)
	}
	defer database.Close()

	dialect, _ := db.DetectDialect(database)
	if err := db.RunMigrations(database, dialect); err != nil {
		logger.Fatalf("执行数据库迁移失败：%v", err)
	}

	// 存量密钥就地升级为 AES-GCM（非致命：失败仅告警，双格式解密回退仍可读取旧密文）。
	if n, err := db.MigrateEncryption(database, cfg.EncryptionKey); err != nil {
		l.Warn("密钥加密迁移未完成", "err", err, "hint", "旧格式密文仍可经兼容解密读取")
	} else if n > 0 {
		l.Info("已就地升级存量密钥为 AES-GCM 加密", "count", n)
	}

	if err := db.SeedProviders(database, dialect); err != nil {
		logger.Fatalf("预置服务商失败：%v", err)
	}

	timeout := 30 * time.Second
	proxyClient := proxy.NewProxyClient(timeout)
	routeEngine := router.NewRouteEngine(database, proxyClient, cfg.EncryptionKey, dialect)

	authHandler := handlers.NewAuthHandler(database, cfg.JWTSecret, cfg.EncryptionKey)
	providerHandler := handlers.NewProviderHandler(database, cfg.EncryptionKey)
	accountHandler := handlers.NewAccountHandler(database, cfg.EncryptionKey)
	chatHandler := handlers.NewChatHandler(database, routeEngine)
	comboHandler := handlers.NewComboHandler(database)
	dashboardHandler := handlers.NewDashboardHandler(database)
	logHandler := handlers.NewLogHandler(database)
	adminHandler := handlers.NewAdminHandler(database)
	ohandler := handlers.NewOAuthHandler(database)
	priceHandler := handlers.NewPriceHandler()
	modelsSyncHandler := handlers.NewModelsSyncHandler(database)
	rotationStatsHandler := handlers.NewRotationStatsHandler(database)
	rtkStatsHandler := handlers.NewRTKStatsHandler()

	// 启动时后台异步同步价格（不阻塞服务器启动）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := price.SyncFromModelsDev(ctx); err != nil {
			logger.Error("价格同步失败", "err", err)
		} else {
			logger.Info("价格同步完成", "count", price.Count())
		}
	}()

	// 每 6 小时后台重试价格同步
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := price.SyncFromModelsDev(ctx); err != nil {
				logger.Warn("价格同步失败（定时）", "err", err)
			}
			cancel()
		}
	}()

	// 模型自动同步：首次立即执行，之后每 30 分钟
	task.StartSyncLoop(database)

	r := gin.Default()
	r.Use(CORSMiddleware())
	// 限制请求体大小，防止超大 body 打爆内存（10 MB 上限）
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
		c.Next()
	})

	r.POST("/api/auth/register", authHandler.Register)
	r.POST("/api/auth/login", authHandler.Login)

	protected := r.Group("/api")
	protected.Use(combinedAuth(database, cfg.JWTSecret))
	{
		protected.GET("/auth/me", authHandler.Me)
		protected.POST("/auth/change-password", authHandler.ChangePassword)
		protected.POST("/auth/api-keys", authHandler.CreateAPIKey)
		protected.GET("/auth/api-keys", authHandler.ListAPIKeys)
		protected.GET("/auth/api-keys/:id/key", authHandler.GetAPIKey)
		protected.PUT("/auth/api-keys/:id", authHandler.ToggleAPIKey)
		protected.DELETE("/auth/api-keys/:id", authHandler.RevokeAPIKey)

		protected.GET("/providers", providerHandler.ListProviders)
		protected.GET("/providers/:id", providerHandler.GetProvider)
		protected.POST("/providers", auth.RequireRole("admin"), providerHandler.CreateProvider)
		protected.PUT("/providers/:id", auth.RequireRole("admin"), providerHandler.UpdateProvider)
		protected.DELETE("/providers/:id", auth.RequireRole("admin"), providerHandler.DeleteProvider)
		protected.POST("/providers/:id/health-check", auth.RequireRole("admin"), providerHandler.HealthCheck)
		protected.GET("/providers/:id/models", providerHandler.GetProviderModels)
		protected.POST("/providers/:id/models/fetch", auth.RequireRole("admin"), providerHandler.FetchProviderModels)
		protected.PUT("/providers/:id/models", auth.RequireRole("admin"), providerHandler.ToggleModel)
		protected.POST("/providers/:id/models/test", auth.RequireRole("admin"), providerHandler.TestModel)
		protected.POST("/providers/:id/models/test-all", auth.RequireRole("admin"), providerHandler.TestAllModels)
		// 多模态能力探测：发一条含小图的真实请求，判定模型是否支持图片输入并持久化
		protected.POST("/providers/:id/models/probe", auth.RequireRole("admin"), providerHandler.ProbeModel)
		protected.POST("/providers/:id/models/probe-all", auth.RequireRole("admin"), providerHandler.ProbeAllModels)

		protected.GET("/accounts", accountHandler.ListAccounts)
		protected.POST("/accounts", accountHandler.CreateAccount)
		protected.POST("/accounts/test", accountHandler.TestAccount)
		protected.PUT("/accounts/:id", accountHandler.UpdateAccount)
		protected.DELETE("/accounts/:id", accountHandler.DeleteAccount)
		protected.GET("/accounts/:id/key", auth.RequireRole("admin"), accountHandler.GetAccountKey)

		protected.GET("/combos", comboHandler.ListCombos)
		protected.POST("/combos", comboHandler.CreateCombo)
		protected.PUT("/combos/:id", comboHandler.UpdateCombo)
		protected.DELETE("/combos/:id", comboHandler.DeleteCombo)

		protected.GET("/dashboard/stats", dashboardHandler.GetStats)
	// 价格同步（管理员）
	protected.POST("/admin/price/sync", auth.RequireRole("admin"), priceHandler.Sync)
	protected.GET("/admin/price/status", auth.RequireRole("admin"), priceHandler.Status)
	// 模型同步（管理员）
	protected.POST("/admin/models/sync", auth.RequireRole("admin"), modelsSyncHandler.Sync)
	protected.GET("/admin/models/sync/status", auth.RequireRole("admin"), modelsSyncHandler.Status)

	// 轮换统计
	protected.GET("/admin/rotation/stats", auth.RequireRole("admin"), rotationStatsHandler.GetStats)
	// RTK 压缩统计
	protected.GET("/admin/rtk/stats", auth.RequireRole("admin"), rtkStatsHandler.GetStats)
	protected.POST("/admin/rtk/test", auth.RequireRole("admin"), rtkStatsHandler.TestCompression)
	protected.POST("/admin/rtk/reset", auth.RequireRole("admin"), rtkStatsHandler.ResetStats)

		protected.GET("/models", chatHandler.AvailableModels)

		protected.GET("/logs", logHandler.ListLogs)
		protected.GET("/logs/:id", logHandler.GetLogDetail)

		protected.GET("/admin/users", auth.RequireRole("admin"), adminHandler.ListUsers)
		protected.PUT("/admin/users/:id/role", auth.RequireRole("admin"), adminHandler.UpdateUserRole)
		protected.PUT("/admin/users/:id", auth.RequireRole("admin"), adminHandler.UpdateUser)
		protected.DELETE("/admin/users/:id", auth.RequireRole("admin"), adminHandler.DeleteUser)
		protected.GET("/admin/settings", auth.RequireRole("admin"), adminHandler.GetSettings)
		protected.PUT("/admin/settings", auth.RequireRole("admin"), adminHandler.UpdateSettings)
	}

	openai := r.Group("/v1")
	openai.Use(auth.APIKeyAuthMiddleware(database))
	{
		openai.POST("/chat/completions", chatHandler.ChatCompletions)
		openai.GET("/models", chatHandler.ModelsList)
		// 原生 Anthropic Messages 入口（Anthropic SDK / Claude Code 可直连）
		openai.POST("/messages", chatHandler.MessagesNative)
	}

	// 原生 Gemini Generative Language 入口（Gemini SDK 可直连）。
	// 路径形如 /v1beta/models/{model}:generateContent 与 :streamGenerateContent，
	// 用通配路由捕获模型名与动作，由 handler 解析。
	gemini := r.Group("/v1beta")
	gemini.Use(auth.APIKeyAuthMiddleware(database))
	{
		gemini.Any("/models/*action", chatHandler.GeminiNative)
	}

	// OAuth 授权路由（公开，无需鉴权）
	oauth := r.Group("/api/oauth")
	{
		oauth.POST("/start", ohandler.StartOAuth)
		oauth.GET("/:provider/callback", ohandler.OAuthCallback)
		oauth.POST("/refresh", ohandler.RefreshToken)
		oauth.GET("/providers", ohandler.ListProviders)
		oauth.GET("/status", ohandler.GetTokenStatus)
	}

	r.StaticFile("/", "../frontend/dist/index.html")
	r.Static("/assets", "../frontend/dist/assets")
	r.Static("/static", "../frontend/dist/static")
	r.NoRoute(func(c *gin.Context) {
		c.File("../frontend/dist/index.html")
	})

	l.Info("服务器已启动", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		logger.Fatalf("启动服务器失败：%v", err)
	}
}
