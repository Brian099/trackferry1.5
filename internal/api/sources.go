package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"trackferry/internal/navidrome"
)

func (s *Server) synologyStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"synology": s.Synology.Status()})
}

func (s *Server) synologyLogin(w http.ResponseWriter, r *http.Request) {
	key := "synology:" + clientKey(r)
	if ok, wait := s.dsLimiter.Allow(key, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "throttled", "失败次数太多，请 15 分钟后再试")
		return
	}
	var in struct {
		Base     string `json:"base"`
		User     string `json:"user"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err := s.Synology.Login(ctx, in.Base, in.User, in.Password, in.OTP)
	in.Password, in.OTP = "", ""
	if err != nil {
		s.dsLimiter.Fail(key, time.Now())
		writeError(w, http.StatusBadGateway, "synology", err.Error())
		return
	}
	s.dsLimiter.Reset(key)
	s.Log.Info("群晖音乐　已连接")
	s.Library.Restart()
	s.synologyStatus(w, r)
}

func (s *Server) synologyLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Synology.Logout(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.Library.Restart()
	s.synologyStatus(w, r)
}

func (s *Server) navidromeStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"navidrome": s.Navidrome.Status()})
}

func (s *Server) navidromeLogin(w http.ResponseWriter, r *http.Request) {
	key := "navidrome:" + clientKey(r)
	if ok, wait := s.ndLimiter.Allow(key, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "throttled", "失败次数太多，请 15 分钟后再试")
		return
	}
	var in struct {
		Base     string `json:"base"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err := s.Navidrome.Login(ctx, in.Base, in.User, in.Password)
	in.Password = ""
	if err != nil {
		s.ndLimiter.Fail(key, time.Now())
		if err == navidrome.ErrAuth {
			writeError(w, http.StatusBadGateway, "navidrome", "Navidrome 没有接受这个用户名和密码")
			return
		}
		writeError(w, http.StatusBadGateway, "navidrome", err.Error())
		return
	}
	s.ndLimiter.Reset(key)
	s.Log.Info("Navidrome　已连接")
	s.Library.Restart()
	s.navidromeStatus(w, r)
}

func (s *Server) navidromeLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Navidrome.Logout(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.Library.Restart()
	s.navidromeStatus(w, r)
}
