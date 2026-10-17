package api

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

const (
	dashboardRoutePrefix  = "/dashboard/"
	dashboardEtagFileName = ".etag"
	defaultDashboardURL   = "https://github.com/SagerNet/sing-box-dashboard/archive/refs/heads/gh-pages.zip"
)

type dashboardStatus int

const (
	dashboardEmpty dashboardStatus = iota
	dashboardManaged
	dashboardUserProvided
)

type dashboard struct {
	ctx            context.Context
	logger         log.ContextLogger
	options        option.APIDashboardOptions
	path           string
	url            string
	updateInterval time.Duration
	fileServer     http.Handler
	httpClient     *http.Client
	// powerGovernor is the fork's sleep authority, or nil where none is installed.
	//
	// A dashboard update is a download of a zip nobody asked for at that moment, on a timer that
	// fires whether or not anybody is looking. Left ungated it wakes the radio on a sleeping device
	// to fetch files that will not be read until the device wakes anyway, which is the exact budget
	// the tunnel exists to save. Deferring it costs the freshness of an operator panel and nothing
	// else - the served files stay on disk and the panel keeps working from the last copy.
	//
	// The FIRST update is deliberately not deferred: on a fresh profile the panel is empty, and it
	// is what the operator is waiting for. That is the same rule the rule-set refresh follows, for a
	// different reason (that one is correctness; this one is the first paint).
	powerGovernor *power.Governor
	lastEtag      string
	lastUpdated   time.Time
}

func newDashboard(ctx context.Context, logger log.ContextLogger, options option.APIDashboardOptions) *dashboard {
	path := options.Path
	if path == "" {
		path = "dashboard"
	}
	path = filemanager.BasePath(ctx, os.ExpandEnv(path))
	url := options.DownloadURL
	if url == "" {
		url = defaultDashboardURL
	}
	updateInterval := 24 * time.Hour
	if options.UpdateInterval > 0 {
		updateInterval = time.Duration(options.UpdateInterval)
	}
	return &dashboard{
		ctx:            ctx,
		logger:         logger,
		options:        options,
		path:           path,
		url:            url,
		updateInterval: updateInterval,
		fileServer:     http.StripPrefix(dashboardRoutePrefix, http.FileServer(dashboardDir(path))),
		powerGovernor:  service.FromContext[*power.Governor](ctx),
	}
}

func (d *dashboard) start(ctx context.Context) error {
	_, err := filemanager.ReadDir(d.ctx, d.path)
	if err != nil && !os.IsNotExist(err) {
		return E.Cause(err, "read dashboard directory")
	}
	status := d.loadState()
	if status == dashboardUserProvided {
		d.logger.Info("dashboard: serving user-provided files at ", d.path, ", auto-update disabled")
		return nil
	}
	httpClientManager := service.FromContext[adapter.HTTPClientManager](d.ctx)
	transport, err := httpClientManager.ResolveTransport(d.ctx, d.logger, common.PtrValueOrDefault(d.options.HTTPClient))
	if err != nil {
		return E.Cause(err, "create dashboard http client")
	}
	d.httpClient = &http.Client{Transport: transport}
	go d.loopUpdate(ctx, status)
	return nil
}

func (d *dashboard) close() error {
	if d.httpClient != nil {
		d.httpClient.CloseIdleConnections()
	}
	return nil
}

func (d *dashboard) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, dashboardRoutePrefix) {
		d.fileServer.ServeHTTP(writer, request)
		return
	}
	http.Redirect(writer, request, dashboardRoutePrefix, http.StatusFound)
}

func (d *dashboard) loopUpdate(ctx context.Context, status dashboardStatus) {
	var nextUpdate time.Time
	if status == dashboardManaged {
		nextUpdate = d.lastUpdated.Add(d.updateInterval)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	initial := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		if !now.Before(nextUpdate) {
			// The sleep gate. `WaitProviderRefresh` is the governor's own category for a
			// provider-shaped download, and it returns immediately when the device is awake, so this
			// costs one uncontended lock on the normal path. Waiting rather than skipping is
			// deliberate: the loop is one goroutine that already exists, so blocking it holds no
			// resource, and it makes the update happen as soon as the device wakes instead of up to
			// a full interval later.
			//
			// The first pass is NOT deferred - see the field's comment.
			if !initial && d.powerGovernor != nil {
				if !d.powerGovernor.WaitProviderRefresh(ctx) {
					return
				}
				// The wait can be long, so the deadline arithmetic below must start from after it.
				now = time.Now()
			}
			initial = false
			err := d.fetch(ctx)
			if err != nil {
				d.logger.Error(E.Cause(err, "update dashboard"))
				nextUpdate = now.Add(d.updateInterval)
			} else {
				nextUpdate = d.lastUpdated.Add(d.updateInterval)
			}
		}
		timer.Reset(max(time.Until(nextUpdate), 0))
	}
}

func (d *dashboard) loadState() dashboardStatus {
	entries, err := filemanager.ReadDir(d.ctx, d.path)
	if err != nil {
		return dashboardEmpty
	}
	if len(entries) == 0 {
		return dashboardEmpty
	}
	etagPath := filepath.Join(d.path, dashboardEtagFileName)
	etagBytes, err := filemanager.ReadFile(d.ctx, etagPath)
	if err != nil {
		return dashboardUserProvided
	}
	d.lastEtag = strings.TrimSpace(string(etagBytes))
	info, err := filemanager.Stat(d.ctx, etagPath)
	if err == nil {
		d.lastUpdated = info.ModTime()
	}
	return dashboardManaged
}

func (d *dashboard) fetch(ctx context.Context) error {
	d.logger.Info("updating dashboard from URL: ", d.url)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return err
	}
	if d.lastEtag != "" {
		request.Header.Set("If-None-Match", d.lastEtag)
	}
	defer d.httpClient.CloseIdleConnections()
	response, err := d.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		d.lastUpdated = time.Now()
		err = filemanager.WriteFile(d.ctx, filepath.Join(d.path, dashboardEtagFileName), []byte(d.lastEtag), 0o644)
		if err != nil {
			d.logger.Warn(E.Cause(err, "save dashboard update time"))
		}
		d.logger.Info("dashboard: not modified")
		return nil
	default:
		return E.New("unexpected status: ", response.Status)
	}
	etag := response.Header.Get("Etag")
	err = d.extract(response.Body, etag)
	if err != nil {
		return err
	}
	d.lastEtag = etag
	d.lastUpdated = time.Now()
	d.logger.Info("dashboard: updated")
	return nil
}

func (d *dashboard) extract(body io.Reader, etag string) error {
	tempFile, err := filemanager.CreateTemp(d.ctx, "sing-box-dashboard-*.zip")
	if err != nil {
		return err
	}
	tempZipPath := tempFile.Name()
	defer filemanager.Remove(d.ctx, tempZipPath)
	_, err = io.Copy(tempFile, body)
	tempFile.Close()
	if err != nil {
		return err
	}
	reader, err := zip.OpenReader(tempZipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	tempDir := d.path + ".tmp"
	err = filemanager.RemoveAll(d.ctx, tempDir)
	if err != nil {
		return err
	}
	err = filemanager.MkdirAll(d.ctx, tempDir, 0o755)
	if err != nil {
		return err
	}
	trimDir := zipIsInSingleDirectory(reader.File)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		pathElements := strings.Split(file.Name, "/")
		if trimDir {
			pathElements = pathElements[1:]
		}
		if len(pathElements) == 0 {
			continue
		}
		relativePath := filepath.Join(pathElements...)
		if !filepath.IsLocal(relativePath) {
			filemanager.RemoveAll(d.ctx, tempDir)
			return E.New("invalid dashboard archive entry: ", file.Name)
		}
		savePath := filepath.Join(tempDir, relativePath)
		err = filemanager.MkdirAll(d.ctx, filepath.Dir(savePath), 0o755)
		if err != nil {
			filemanager.RemoveAll(d.ctx, tempDir)
			return err
		}
		err = extractZipEntry(d.ctx, file, savePath)
		if err != nil {
			filemanager.RemoveAll(d.ctx, tempDir)
			return err
		}
	}
	err = filemanager.WriteFile(d.ctx, filepath.Join(tempDir, dashboardEtagFileName), []byte(etag), 0o644)
	if err != nil {
		filemanager.RemoveAll(d.ctx, tempDir)
		return err
	}
	err = filemanager.RemoveAll(d.ctx, d.path)
	if err != nil {
		return err
	}
	return filemanager.Rename(d.ctx, tempDir, d.path)
}

func extractZipEntry(ctx context.Context, file *zip.File, savePath string) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	writer, err := filemanager.Create(ctx, savePath)
	if err != nil {
		return err
	}
	defer writer.Close()
	_, err = io.Copy(writer, reader)
	return err
}

// GitHub archives wrap every file under a single "<repo>-<branch>/" top-level directory.
func zipIsInSingleDirectory(files []*zip.File) bool {
	var dirName string
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		pathElements := strings.Split(file.Name, "/")
		if len(pathElements) < 2 {
			return false
		}
		if dirName == "" {
			dirName = pathElements[0]
		} else if dirName != pathElements[0] {
			return false
		}
	}
	return dirName != ""
}
