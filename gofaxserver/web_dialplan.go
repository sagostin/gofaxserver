package gofaxserver

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/kataras/iris/v12"
)

// Admin handlers for the database-backed dialplan (dialplan.source = "db").
// Mutations take effect immediately in db mode via reloadDialplan; in config
// mode they are stored but inactive until the source is switched.

func validateDialplanRule(r *DialplanRule) error {
	if _, err := regexp.Compile(r.Pattern); err != nil {
		return fmt.Errorf("invalid pattern %q: %w", r.Pattern, err)
	}
	return nil
}

func (s *Server) handleGetDialplan(ctx iris.Context) {
	var rules []DialplanRule
	if err := s.DB.Order("position ASC").Find(&rules).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to list dialplan rules: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{
		"source": dialplanSource(),
		"rules":  rules,
	})
}

func (s *Server) handleCreateDialplanRule(ctx iris.Context) {
	var rule DialplanRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if err := validateDialplanRule(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	if rule.Position == 0 {
		var maxPos int
		s.DB.Model(&DialplanRule{}).Select("COALESCE(MAX(position), -1)").Scan(&maxPos)
		rule.Position = maxPos + 1
	}
	if err := s.DB.Create(&rule).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to create rule: " + err.Error()})
		return
	}
	if err := s.reloadDialplan(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule saved but reload failed: " + err.Error()})
		return
	}
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(rule)
}

func (s *Server) handleUpdateDialplanRule(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid rule id"})
		return
	}
	var rule DialplanRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if err := validateDialplanRule(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	rule.ID = uint(id)
	if err := s.DB.Save(&rule).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update rule: " + err.Error()})
		return
	}
	if err := s.reloadDialplan(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule saved but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(rule)
}

func (s *Server) handleDeleteDialplanRule(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid rule id"})
		return
	}
	if err := s.DB.Delete(&DialplanRule{}, id).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete rule: " + err.Error()})
		return
	}
	if err := s.reloadDialplan(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule deleted but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

func (s *Server) handleReorderDialplanRules(ctx iris.Context) {
	var order []struct {
		ID       uint `json:"id"`
		Position int  `json:"position"`
	}
	if err := ctx.ReadJSON(&order); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	for _, o := range order {
		if err := s.DB.Model(&DialplanRule{}).Where("id = ?", o.ID).Update("position", o.Position).Error; err != nil {
			ctx.StatusCode(http.StatusInternalServerError)
			ctx.JSON(iris.Map{"error": "failed to reorder: " + err.Error()})
			return
		}
	}
	if err := s.reloadDialplan(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "reordered but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}
