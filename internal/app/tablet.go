// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"github.com/ddvk/rmfakecloud/internal/messages"
	"github.com/gin-gonic/gin"
	"net/http"
)

// Tablet mode deliberately exposes only the document protocol, on loopback.
func (app *App) registerTabletRoutes(r *gin.Engine) {
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"service": "remarkable-drive-cloud"}) })
	r.GET("/discovery/v1/endpoints", func(c *gin.Context) {
		c.JSON(200, messages.EndpointsResponse{Notifications: app.cfg.CloudHost, Webapp: app.cfg.CloudHost, MQTT: app.cfg.CloudHost})
	})
	r.GET("/discovery/v1/webapp", func(c *gin.Context) { c.JSON(200, messages.HostResponse{Host: app.cfg.CloudHost, Status: "OK"}) })
	r.GET("/service/json/1/:service", app.locateService)
	r.POST("/token/json/2/device/new", app.newDevice)
	r.POST("/token/json/2/user/new", app.newUserToken)
	r.POST("/token/json/2/device/delete", app.deleteDevice)
	r.POST("/token/json/3/device/delete", app.deleteDevice)
	r.GET("/mqtt", app.handleMQTTWebSocket)
	r.POST("/sync/reports/v1", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	a := r.Group("/")
	a.Use(app.authMiddleware())
	a.GET("/notifications/ws/json/1", app.connectWebSocket)
	a.GET("/sync/v3/root", app.syncGetRootV3)
	a.GET("/sync/v4/root", app.syncGetRootV4)
	a.PUT("/sync/v3/root", app.syncUpdateRootV3)
	a.GET("/sync/v3/files/:file", app.blobStorageRead)
	a.PUT("/sync/v3/files/:file", app.blobStorageWrite)
	a.POST("/sync/v3/check-files", app.checkFilesPresence)
	a.GET("/sync/v3/missing", app.checkMissingBlob)
	a.POST("/sync/v2/sync-complete", app.syncCompleteV2)
	a.POST("/api/v1/sync-complete", app.syncComplete)
	a.POST("/sync/v2/signed-urls/downloads", app.blobStorageDownload)
	a.POST("/sync/v2/signed-urls/uploads", app.blobStorageUpload)
	a.POST("/api/v1/signed-urls/downloads", app.blobStorageDownload)
	a.POST("/api/v1/signed-urls/uploads", app.blobStorageUpload)
	a.GET("/integrations/v2/instances", func(c *gin.Context) { c.JSON(200, []any{}) })
}
