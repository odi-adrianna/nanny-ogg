// Package server wires the HTTP routes for the nanny-ogg tool server and
// exposes them as an OpenAPI tool server for Open WebUI.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/odi-adrianna/nanny-ogg/internal/mealie"
	"github.com/odi-adrianna/nanny-ogg/internal/plan"
)

const maxRecipes = 25

// Mealie is the part of the Mealie client the server uses (faked in tests).
type Mealie interface {
	SearchRecipes(ctx context.Context, query string, perPage int) ([]mealie.Summary, error)
	GetRecipe(ctx context.Context, slug string) (*mealie.Recipe, error)
	ImportFromURL(ctx context.Context, recipeURL string) (string, error)
	ListMealPlans(ctx context.Context, start, end string) ([]mealie.MealPlanEntry, error)
	CreateMealPlanEntry(ctx context.Context, e mealie.MealPlanEntry) (*mealie.MealPlanEntry, error)
	DeleteMealPlanEntry(ctx context.Context, id int) error
	CreateShoppingList(ctx context.Context, name string, items []string) (string, error)
}

// Config holds runtime configuration for the HTTP layer.
type Config struct {
	APIKey      string         // if set, required as "Authorization: Bearer <key>" on tool endpoints
	AllowOrigin string         // CORS Access-Control-Allow-Origin (defaults to reflecting the caller)
	Location    *time.Location // household time zone for "today"
}

type Server struct {
	log     *slog.Logger
	mealie  Mealie
	profile *plan.Profile
	loc     *time.Location
	now     func() time.Time
}

// New returns the HTTP handler for the tool server.
func New(log *slog.Logger, mc Mealie, prof *plan.Profile, cfg Config) http.Handler {
	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	s := &Server{log: log, mealie: mc, profile: prof, loc: loc, now: time.Now}
	return s.handler(cfg)
}

func (s *Server) handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("GET /openapi.json", s.openapi)
	mux.HandleFunc("GET /profile", s.getProfile)
	mux.HandleFunc("GET /recipes/search", s.searchRecipes)
	mux.HandleFunc("GET /recipes/{slug}", s.getRecipe)
	mux.HandleFunc("POST /recipes/import", s.importRecipe)
	mux.HandleFunc("POST /portions", s.portions)
	mux.HandleFunc("POST /day-check", s.dayCheck)
	mux.HandleFunc("GET /mealplan", s.listMealPlan)
	mux.HandleFunc("GET /mealplan/today", s.todayMealPlan)
	mux.HandleFunc("POST /mealplan", s.writeMealPlan)
	mux.HandleFunc("POST /mealplan/delete", s.deleteMealPlan)
	mux.HandleFunc("POST /shopping-list", s.shoppingList)
	return middleware(s.log, cfg, mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPISpec))
}

func (s *Server) today() string { return s.now().In(s.loc).Format("2006-01-02") }

func (s *Server) getProfile(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profile": s.profile, "today": s.today(), "weekday": s.now().In(s.loc).Weekday().String()})
}

// ---------------------------------------------------------------------------
// Recipes

type recipeResult struct {
	Slug              string   `json:"slug"`
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Categories        []string `json:"categories,omitempty"`
	Servings          float64  `json:"servings,omitempty"`
	TotalTime         string   `json:"total_time,omitempty"`
	PrepTime          string   `json:"prep_time,omitempty"`
	CookTime          string   `json:"cook_time,omitempty"`
	Rating            float64  `json:"rating,omitempty"`
	KcalPerServing    float64  `json:"kcal_per_serving,omitempty"`
	ProteinPerServing float64  `json:"protein_per_serving,omitempty"`
	KcalSource        string   `json:"kcal_source"` // mealie | missing
	Source            string   `json:"source,omitempty"`
	Ingredients       []string `json:"ingredients,omitempty"`
	Steps             []string `json:"steps,omitempty"`
}

func toResult(rec *mealie.Recipe, detail bool) recipeResult {
	res := recipeResult{
		Slug:        rec.Slug,
		Name:        rec.Name,
		Description: rec.Description,
		Servings:    rec.Servings(),
		TotalTime:   string(rec.TotalTime),
		PrepTime:    string(rec.PrepTime),
		CookTime:    string(rec.PerformTime),
		Rating:      rec.Rating,
		Source:      rec.OrgURL,
		KcalSource:  "missing",
	}
	for _, t := range rec.Tags {
		res.Tags = append(res.Tags, t.Name)
	}
	for _, c := range rec.RecipeCategory {
		res.Categories = append(res.Categories, c.Name)
	}
	if n := rec.Nutrition; n != nil {
		if v, ok := n.Calories.Number(); ok {
			res.KcalPerServing = v
			res.KcalSource = "mealie"
		}
		if v, ok := n.ProteinContent.Number(); ok {
			res.ProteinPerServing = v
		}
	}
	// Ingredients are always returned: they are needed to estimate missing
	// kcal/protein and to build the shopping list.
	for _, ing := range rec.RecipeIngredient {
		if t := strings.TrimSpace(ing.Text()); t != "" {
			res.Ingredients = append(res.Ingredients, t)
		}
	}
	if detail {
		for _, st := range rec.RecipeInstructions {
			if t := strings.TrimSpace(st.Text); t != "" {
				res.Steps = append(res.Steps, t)
			}
		}
	}
	return res
}

func (s *Server) searchRecipes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	max := 10
	if v := r.URL.Query().Get("max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			max = n
		}
	}
	if max > maxRecipes {
		max = maxRecipes
	}
	sums, err := s.mealie.SearchRecipes(r.Context(), q, max)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie search", err)
		return
	}
	out := []recipeResult{}
	for _, sum := range sums {
		rec, err := s.mealie.GetRecipe(r.Context(), sum.Slug)
		if err != nil {
			s.log.Warn("skip recipe", "slug", sum.Slug, "err", err)
			continue
		}
		out = append(out, toResult(rec, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipes": out})
}

func (s *Server) getRecipe(w http.ResponseWriter, r *http.Request) {
	rec, err := s.mealie.GetRecipe(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie get recipe", err)
		return
	}
	writeJSON(w, http.StatusOK, toResult(rec, true))
}

type importReq struct {
	Confirm bool   `json:"confirm"`
	URL     string `json:"url"`
}

func (s *Server) importRecipe(w http.ResponseWriter, r *http.Request) {
	var req importReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		refuse(w, "refusing to write: set confirm=true only after the user explicitly approves saving this recipe to Mealie")
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		badRequest(w, "url must be a full http(s) link to a recipe page")
		return
	}
	slug, err := s.mealie.ImportFromURL(r.Context(), u.String())
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie import from url", err)
		return
	}
	rec, err := s.mealie.GetRecipe(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "slug": slug})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "recipe": toResult(rec, true)})
}

// ---------------------------------------------------------------------------
// Portions & day check

func (s *Server) portions(w http.ResponseWriter, r *http.Request) {
	var req plan.PortionsRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.profile.Portions(req)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) dayCheck(w http.ResponseWriter, r *http.Request) {
	var req plan.DayRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.profile.Day(req)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------------------
// Meal plan (Mealie calendar = source of truth)

type planEntry struct {
	ID         int    `json:"id"`
	Date       string `json:"date"`
	Meal       string `json:"meal"`
	Title      string `json:"title,omitempty"`
	Note       string `json:"note,omitempty"`
	RecipeSlug string `json:"recipe_slug,omitempty"`
	RecipeName string `json:"recipe_name,omitempty"`
}

func toPlanEntry(e mealie.MealPlanEntry) planEntry {
	pe := planEntry{ID: e.ID, Date: e.Date, Meal: e.EntryType, Title: e.Title, Note: e.Text}
	if e.Recipe != nil {
		pe.RecipeSlug, pe.RecipeName = e.Recipe.Slug, e.Recipe.Name
	}
	return pe
}

func validDate(d string) bool {
	_, err := time.Parse("2006-01-02", d)
	return err == nil
}

func (s *Server) listRange(ctx context.Context, start, end string) ([]planEntry, error) {
	items, err := s.mealie.ListMealPlans(ctx, start, end)
	if err != nil {
		return nil, err
	}
	out := []planEntry{}
	for _, e := range items {
		out = append(out, toPlanEntry(e))
	}
	return out, nil
}

func (s *Server) listMealPlan(w http.ResponseWriter, r *http.Request) {
	start, end := r.URL.Query().Get("start"), r.URL.Query().Get("end")
	if start == "" {
		start = s.today()
	}
	if end == "" {
		end = start
	}
	if !validDate(start) || !validDate(end) || end < start {
		badRequest(w, "start and end must be YYYY-MM-DD and end must not be before start")
		return
	}
	out, err := s.listRange(r.Context(), start, end)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie list meal plans", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": start, "end": end, "entries": out})
}

func (s *Server) todayMealPlan(w http.ResponseWriter, r *http.Request) {
	d := s.today()
	out, err := s.listRange(r.Context(), d, d)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie list meal plans", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": d, "entries": out})
}

var validMeals = map[string]bool{"breakfast": true, "lunch": true, "dinner": true, "side": true, "snack": true}

type writeEntry struct {
	Date       string `json:"date"`
	Meal       string `json:"meal"`
	RecipeSlug string `json:"recipe_slug"`
	Title      string `json:"title"`
	Note       string `json:"note"`
}

type writePlanReq struct {
	Confirm         bool         `json:"confirm"`
	ReplaceExisting bool         `json:"replace_existing"`
	Entries         []writeEntry `json:"entries"`
}

func slotKey(date, meal string) string { return date + "/" + meal }

func (s *Server) writeMealPlan(w http.ResponseWriter, r *http.Request) {
	var req writePlanReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		refuse(w, "refusing to write: show the plan to the user first and set confirm=true only after they explicitly approve it")
		return
	}
	if len(req.Entries) == 0 {
		badRequest(w, "entries must not be empty")
		return
	}
	start, end := "", ""
	for i := range req.Entries {
		e := &req.Entries[i]
		e.Meal = strings.ToLower(strings.TrimSpace(e.Meal))
		if !validDate(e.Date) || !validMeals[e.Meal] {
			badRequest(w, "each entry needs date YYYY-MM-DD and meal (lunch, dinner, breakfast, side or snack)")
			return
		}
		if strings.TrimSpace(e.RecipeSlug) == "" && strings.TrimSpace(e.Title) == "" {
			badRequest(w, "each entry needs recipe_slug (a Mealie recipe) or a title")
			return
		}
		if start == "" || e.Date < start {
			start = e.Date
		}
		if e.Date > end {
			end = e.Date
		}
	}

	// Safeguard against overwriting entries added by hand or by the other
	// person: read what is already in those slots first.
	existing, err := s.mealie.ListMealPlans(r.Context(), start, end)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie list meal plans", err)
		return
	}
	bySlot := map[string][]mealie.MealPlanEntry{}
	for _, e := range existing {
		k := slotKey(e.Date, e.EntryType)
		bySlot[k] = append(bySlot[k], e)
	}
	conflicts := []planEntry{}
	seen := map[int]bool{}
	for _, e := range req.Entries {
		for _, ex := range bySlot[slotKey(e.Date, e.Meal)] {
			if !seen[ex.ID] {
				seen[ex.ID] = true
				conflicts = append(conflicts, toPlanEntry(ex))
			}
		}
	}
	if len(conflicts) > 0 && !req.ReplaceExisting {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "some days already have entries in Mealie; nothing was written. Show these to the user and ask whether to replace them (then retry with replace_existing=true) or to skip those slots.",
			"conflicts": conflicts,
		})
		return
	}

	// Resolve recipes before writing anything, so a bad slug writes nothing.
	resolved := make([]mealie.MealPlanEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		me := mealie.MealPlanEntry{Date: e.Date, EntryType: e.Meal, Title: strings.TrimSpace(e.Title), Text: strings.TrimSpace(e.Note)}
		if slug := strings.TrimSpace(e.RecipeSlug); slug != "" {
			rec, err := s.mealie.GetRecipe(r.Context(), slug)
			if err != nil {
				var ae *mealie.APIError
				if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
					badRequest(w, "recipe not found in Mealie: "+slug+" (import it first). Nothing was written.")
					return
				}
				s.fail(w, http.StatusBadGateway, "mealie get recipe", err)
				return
			}
			me.RecipeID = rec.ID
		}
		resolved = append(resolved, me)
	}

	replaced := []planEntry{}
	if req.ReplaceExisting {
		for _, c := range conflicts {
			if err := s.mealie.DeleteMealPlanEntry(r.Context(), c.ID); err != nil {
				s.fail(w, http.StatusBadGateway, "mealie delete meal plan entry", err)
				return
			}
			replaced = append(replaced, c)
		}
	}
	created := []planEntry{}
	for _, me := range resolved {
		out, err := s.mealie.CreateMealPlanEntry(r.Context(), me)
		if err != nil {
			s.log.Error("mealie create meal plan entry", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":    "Mealie stopped accepting entries part-way; tell the user which ones were saved",
				"created":  created,
				"replaced": replaced,
			})
			return
		}
		created = append(created, toPlanEntry(*out))
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "created": created, "replaced": replaced})
}

type deletePlanReq struct {
	Confirm bool  `json:"confirm"`
	IDs     []int `json:"ids"`
}

func (s *Server) deleteMealPlan(w http.ResponseWriter, r *http.Request) {
	var req deletePlanReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		refuse(w, "refusing to delete: set confirm=true only after the user explicitly approves removing these entries")
		return
	}
	if len(req.IDs) == 0 {
		badRequest(w, "ids must not be empty")
		return
	}
	deleted := []int{}
	for _, id := range req.IDs {
		if err := s.mealie.DeleteMealPlanEntry(r.Context(), id); err != nil {
			s.log.Error("mealie delete meal plan entry", "id", id, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Mealie refused a delete", "deleted": deleted, "failed_id": id})
			return
		}
		deleted = append(deleted, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// ---------------------------------------------------------------------------
// Shopping list

type shoppingReq struct {
	Confirm bool     `json:"confirm"`
	Name    string   `json:"name"`
	Items   []string `json:"items"`
}

func (s *Server) shoppingList(w http.ResponseWriter, r *http.Request) {
	var req shoppingReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		refuse(w, "refusing to write: set confirm=true only after the user asks to save the shopping list to Mealie")
		return
	}
	if len(req.Items) == 0 {
		badRequest(w, "items must not be empty")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Nanny Ogg — " + s.today()
	}
	id, err := s.mealie.CreateShoppingList(r.Context(), name, req.Items)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "mealie create shopping list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "list_id": id, "name": name, "items": len(req.Items)})
}

// ---------------------------------------------------------------------------
// helpers

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		badRequest(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func refuse(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusPreconditionRequired, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, code int, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeJSON(w, code, map[string]string{"error": msg})
}

// protectedPath reports whether a path requires the API key. Health and the
// OpenAPI document stay open; unknown paths fall through to a clean 404.
func protectedPath(p string) bool {
	for _, prefix := range []string{"/profile", "/recipes", "/portions", "/day-check", "/mealplan", "/shopping-list"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// setCORS reflects the caller's Origin so the tool server works regardless of
// which host Open WebUI is served from. A configured non-"*" AllowOrigin pins
// that value instead.
func setCORS(w http.ResponseWriter, r *http.Request, cfg Config) {
	w.Header().Set("Vary", "Origin")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	}
	w.Header().Set("Access-Control-Max-Age", "86400")

	reqOrigin := r.Header.Get("Origin")
	switch {
	case cfg.AllowOrigin != "" && cfg.AllowOrigin != "*":
		w.Header().Set("Access-Control-Allow-Origin", cfg.AllowOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	case reqOrigin != "":
		w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	default:
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// middleware adds CORS, optional bearer-key auth and request logging.
func middleware(log *slog.Logger, cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r, cfg)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if cfg.APIKey != "" && protectedPath(r.URL.Path) {
			want := "Bearer " + cfg.APIKey
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				log.Warn("request", "method", r.Method, "path", r.URL.Path, "status", http.StatusUnauthorized)
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status)
	})
}
