package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-logr/logr"
	"go.seankhliao.com/svcrunner"
	"go.seankhliao.com/svcrunner/envflag"
	"go.seankhliao.com/webstyle/webstatic"
)

type Server struct {
	src             string
	redirectCsvPath string
	redirects       map[string]redirect
	log             logr.Logger
}

type redirect struct {
	code int
	loc  string
}

func New(hs *http.Server) *Server {
	s := &Server{
		redirects: make(map[string]redirect),
	}
	mux := http.NewServeMux()
	mux.Handle("/", s)
	webstatic.Register(mux)
	hs.Handler = mux
	return s
}

func (s *Server) Register(c *envflag.Config) {
	c.StringVar(&s.src, "webserve.src", "src", "source to serve")
	c.StringVar(&s.redirectCsvPath, "webserve.redirects", "", "path to csv file of redirects code,old,new")
}

func (s *Server) Init(ctx context.Context, t svcrunner.Tools) error {
	s.log = t.Log.WithName("webserve")

	if s.redirectCsvPath != "" {
		b, err := os.ReadFile(s.redirectCsvPath)
		if err != nil {
			return fmt.Errorf("read redirect csv=%v: %w", s.redirectCsvPath, err)
		}
		recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
		if err != nil {
			return fmt.Errorf("parse redirect csv=%v: %w", s.redirectCsvPath, err)
		}
		for i, rec := range recs {
			code, err := strconv.Atoi(rec[0])
			if err != nil {
				return fmt.Errorf("parse redirect csv line=%v code=%v: %w", i, rec[0], err)
			}
			s.redirects[rec[1]] = redirect{code, rec[2]}
		}
	}
	return nil
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if red, ok := s.redirects[r.URL.Path]; ok {
		http.Redirect(rw, r, red.loc, red.code)
		s.log.Info("redirected", "status", red.code, "url", r.URL.String(), "user_agent", r.UserAgent(), "referrer", r.Referer())
		return
	}
	fp, code := func() (string, int) {
		pth := r.URL.Path
		if pth[len(pth)-1] == '/' {
			fp := filepath.Join(s.src, pth[:len(pth)-1]+".html")
			fi, err := os.Stat(fp)
			if err == nil && !fi.IsDir() {
				return fp, http.StatusOK
			}
			fp = filepath.Join(s.src, pth, "index.html")
			fi, err = os.Stat(fp)
			if err == nil && !fi.IsDir() {
				return fp, http.StatusOK
			}
		} else {
			fp := filepath.Join(s.src, pth)
			fi, err := os.Stat(fp)
			if err == nil && !fi.IsDir() {
				return fp, http.StatusOK
			}
		}
		return filepath.Join(s.src, "404.html"), http.StatusNotFound
	}()

	f, err := os.Open(fp)
	if err != nil {
		http.Error(rw, "oops", http.StatusInternalServerError)
		s.log.Error(err, "open file", "file", fp)
		return
	}
	defer f.Close()

	ct := mime.TypeByExtension(filepath.Ext(fp))
	if ct == "" {
		buf := make([]byte, 512)
		n, err := f.Read(buf)
		if err != nil {
			http.Error(rw, "oops", http.StatusInternalServerError)
			s.log.Error(err, "read file for content-type", "file", fp)
			return
		}
		ct = http.DetectContentType(buf[:n])
		f.Seek(0, 0)
	}

	rw.Header().Set("content-type", ct)
	rw.WriteHeader(code)

	n, err := io.Copy(rw, f)
	if err != nil {
		s.log.Error(err, "writing response", "bytes", n)
		return
	}

	s.log.Info("served", "status", code, "url", r.URL.String(), "user_agent", r.UserAgent(), "referrer", r.Referer(), "bytes", n)
}
