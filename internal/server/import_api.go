package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/importer"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// WordPress WXR import API (ticket 08c, 「WordPress 匯入」). Every route is
// admin only. The run is interactive only, as in backup_api.go: it writes
// the site's content in bulk, so a bearer token cannot start it.

// registerImportRoutes mounts the import routes.
func registerImportRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	e.POST("/api/admin/import/wxr", handleImportUpload, admin)
	e.GET("/api/admin/import", handleImportList, admin)
	e.GET("/api/admin/import/:id", handleImportGet, admin)
	e.PUT("/api/admin/import/:id/mapping", handleImportMapping, admin)
	e.POST("/api/admin/import/:id/dry-run", handleImportDryRun, admin)
	e.POST("/api/admin/import/:id/run", handleImportRun, browserSessionOnly, admin)
	e.POST("/api/admin/import/:id/cancel", handleImportCancel, admin)
}

// importError maps an importer error to a status code. Unknown errors are
// logged and hidden from the response.
func importError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, importer.ErrJobNotFound):
		return infraError(c, http.StatusNotFound, "Import not found")
	case errors.Is(err, importer.ErrJobBusy):
		return infraError(c, http.StatusConflict, "Import is in a state that does not allow this action")
	case errors.Is(err, importer.ErrTooLarge):
		return infraError(c, http.StatusRequestEntityTooLarge, "WXR file exceeds 512 MiB")
	case errors.Is(err, importer.ErrUnsupportedFile):
		return infraError(c, http.StatusBadRequest, "Upload a .xml, .xml.gz or .zip file")
	case errors.Is(err, importer.ErrNotWXR):
		return infraError(c, http.StatusBadRequest, "The file is not a WordPress WXR export")
	case errors.Is(err, importer.ErrLimitExceeded):
		return infraError(c, http.StatusRequestEntityTooLarge, "The export exceeds the parser limits")
	}
	log.Printf("import: %v", err)
	return infraError(c, http.StatusInternalServerError, "Import failed")
}

func importIDParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

// importHost wires the server pieces the importer uses: permalinks, reserved
// slugs and author invitations.
func importHost(ctx context.Context, actor uint) importer.Host {
	db := data.DB.WithContext(ctx)
	return importer.Host{
		Actor: actor,
		Reserved: func(ctx context.Context, slug string) bool {
			return reservedPageSlug(ctx, slug)
		},
		PathFor: func(post core.Post) string {
			return permalinkPathFor(db, loadPermalinkConfig(ctx), post)
		},
		Author: func(ctx context.Context, email, role, note string) (uint, error) {
			var existing core.User
			err := db.WithContext(ctx).Where("LOWER(email) = ?", email).First(&existing).Error
			if err == nil {
				return existing.ID, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, err
			}
			// The invitation link is not shown here. Resend it from the users page.
			link, err := createUserInvitation(db, actor, email, role, note)
			if err != nil {
				return 0, err
			}
			return link.UserID, nil
		},
	}
}

// POST /api/admin/import/wxr (multipart, field file)

func handleImportUpload(c echo.Context) error {
	if c.Request().ContentLength > importer.MaxFileBytes {
		return infraError(c, http.StatusRequestEntityTooLarge, "WXR file exceeds 512 MiB")
	}
	reader, err := c.Request().MultipartReader()
	if err != nil {
		return infraError(c, http.StatusBadRequest, "Upload the export in the file field")
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return infraError(c, http.StatusBadRequest, "Invalid upload")
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		job, err := importer.Upload(ctx, data.DB.WithContext(ctx), actor, part.FileName(), part)
		_ = part.Close()
		if err != nil {
			return importError(c, err)
		}
		syslog.Audit(ctx, actor, "import.upload", map[string]any{"import_id": job.ID, "bytes": job.SourceBytes})
		return c.JSON(http.StatusCreated, importer.View(job))
	}
	return infraError(c, http.StatusBadRequest, "The file field is required")
}

// GET /api/admin/import

func handleImportList(c echo.Context) error {
	jobs, err := importer.ListJobs(data.DB.WithContext(c.Request().Context()), 50)
	if err != nil {
		return importError(c, err)
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, importer.View(job))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// GET /api/admin/import/:id

func handleImportGet(c echo.Context) error {
	id, ok := importIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Import not found")
	}
	job, err := importer.LoadJob(data.DB.WithContext(c.Request().Context()), id)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, importer.View(job))
}

// PUT /api/admin/import/:id/mapping {types, fetch_attachments, author_roles}

type importMappingRequest struct {
	Types            []string          `json:"types"`
	FetchAttachments *bool             `json:"fetch_attachments"`
	AuthorRoles      map[string]string `json:"author_roles"`
}

func handleImportMapping(c echo.Context) error {
	id, ok := importIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Import not found")
	}
	var req importMappingRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	m := importer.Mapping{
		Types:            []string{"post", "page", "attachment", "comment"},
		FetchAttachments: true,
		AuthorRoles:      map[string]string{},
	}
	if req.Types != nil {
		m.Types = req.Types
	}
	for _, t := range m.Types {
		if !importer.ValidType(t) {
			return infraError(c, http.StatusBadRequest, "Unknown import type: "+t)
		}
	}
	if req.FetchAttachments != nil {
		m.FetchAttachments = *req.FetchAttachments
	}
	for login, role := range req.AuthorRoles {
		if len(login) > 200 {
			return infraError(c, http.StatusBadRequest, "Author login is too long")
		}
		if !validUserRole(role) {
			return infraError(c, http.StatusBadRequest, "Unknown role for author "+login)
		}
		m.AuthorRoles[login] = role
	}
	ctx := c.Request().Context()
	job, err := importer.SetMapping(data.DB.WithContext(ctx), id, m)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, importer.View(job))
}

// POST /api/admin/import/:id/dry-run

func handleImportDryRun(c echo.Context) error {
	id, ok := importIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Import not found")
	}
	ctx := c.Request().Context()
	job, _, err := importer.DryRun(ctx, data.DB.WithContext(ctx), id, importHost(ctx, infraActor(c)))
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, importer.View(job))
}

// POST[I] /api/admin/import/:id/run

func handleImportRun(c echo.Context) error {
	id, ok := importIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Import not found")
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	job, rep, err := importer.Run(ctx, data.DB.WithContext(ctx), id, importHost(ctx, actor))
	if err != nil {
		if job.ID == 0 {
			return importError(c, err)
		}
		log.Printf("import %d failed: %v", id, err)
		syslog.Audit(ctx, actor, "import.run", map[string]any{"import_id": id, "state": job.State})
		return c.JSON(http.StatusInternalServerError, importer.View(job))
	}
	syslog.Audit(ctx, actor, "import.run", map[string]any{"import_id": id, "state": job.State, "created": rep.Created})
	return c.JSON(http.StatusOK, importer.View(job))
}

// POST /api/admin/import/:id/cancel

func handleImportCancel(c echo.Context) error {
	id, ok := importIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Import not found")
	}
	ctx := c.Request().Context()
	job, err := importer.Cancel(data.DB.WithContext(ctx), id)
	if err != nil {
		return importError(c, err)
	}
	syslog.Audit(ctx, infraActor(c), "import.cancel", map[string]any{"import_id": id})
	return c.JSON(http.StatusOK, importer.View(job))
}
