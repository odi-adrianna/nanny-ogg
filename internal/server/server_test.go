package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/odi-adrianna/nanny-ogg/internal/mealie"
	"github.com/odi-adrianna/nanny-ogg/internal/plan"
)

type fakeMealie struct {
	recipes map[string]*mealie.Recipe
	plans   []mealie.MealPlanEntry
	nextID  int
	deleted []int
	lists   map[string][]string
}

func newFake() *fakeMealie {
	return &fakeMealie{
		recipes: map[string]*mealie.Recipe{
			"chili": {ID: "uuid-chili", Slug: "chili", Name: "Chili", RecipeServings: 4,
				Nutrition:        &mealie.Nutrition{Calories: "520 kcal", ProteinContent: "35"},
				RecipeIngredient: []mealie.Ingredient{{Display: "500 g wołowiny"}}},
			"salad": {ID: "uuid-salad", Slug: "salad", Name: "Salad", RecipeServings: 2,
				RecipeIngredient: []mealie.Ingredient{{Quantity: 1, Food: &mealie.Food{Name: "cucumber"}}}},
		},
		nextID: 100,
		lists:  map[string][]string{},
	}
}

func (f *fakeMealie) SearchRecipes(_ context.Context, _ string, _ int) ([]mealie.Summary, error) {
	return []mealie.Summary{{Slug: "chili"}, {Slug: "salad"}}, nil
}

func (f *fakeMealie) GetRecipe(_ context.Context, slug string) (*mealie.Recipe, error) {
	if r, ok := f.recipes[slug]; ok {
		return r, nil
	}
	return nil, &mealie.APIError{Status: 404}
}

func (f *fakeMealie) ImportFromURL(_ context.Context, u string) (string, error) {
	f.recipes["imported"] = &mealie.Recipe{ID: "uuid-imp", Slug: "imported", Name: "Imported", OrgURL: u}
	return "imported", nil
}

func (f *fakeMealie) ListMealPlans(_ context.Context, start, end string) ([]mealie.MealPlanEntry, error) {
	out := []mealie.MealPlanEntry{}
	for _, e := range f.plans {
		if e.Date >= start && e.Date <= end {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeMealie) CreateMealPlanEntry(_ context.Context, e mealie.MealPlanEntry) (*mealie.MealPlanEntry, error) {
	f.nextID++
	e.ID = f.nextID
	f.plans = append(f.plans, e)
	return &e, nil
}

func (f *fakeMealie) DeleteMealPlanEntry(_ context.Context, id int) error {
	f.deleted = append(f.deleted, id)
	kept := f.plans[:0]
	for _, e := range f.plans {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	f.plans = kept
	return nil
}

func (f *fakeMealie) CreateShoppingList(_ context.Context, name string, items []string) (string, error) {
	f.lists[name] = items
	return "list-1", nil
}

func newTestServer(t *testing.T, f *fakeMealie, key string) http.Handler {
	t.Helper()
	prof, err := plan.Load("")
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Warsaw")
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil)), mealie: f, profile: prof, loc: loc,
		now: func() time.Time { return time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC) }} // 00:30 Tuesday in Warsaw
	return s.handler(Config{APIKey: key})
}

func do(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestOpenAPIIsValidJSON(t *testing.T) {
	if !json.Valid([]byte(openAPISpec)) {
		t.Fatal("openAPISpec is not valid JSON")
	}
}

func TestAuthRequired(t *testing.T) {
	h := newTestServer(t, newFake(), "secret")
	if code, _ := do(t, h, "GET", "/mealplan/today", nil); code != http.StatusUnauthorized {
		t.Errorf("no key: %d", code)
	}
	if code, _ := do(t, h, "GET", "/healthz", nil); code != http.StatusOK {
		t.Errorf("healthz: %d", code)
	}
	req := httptest.NewRequest("GET", "/mealplan/today", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("with key: %d", rec.Code)
	}
}

func TestSearchReportsKcalSource(t *testing.T) {
	h := newTestServer(t, newFake(), "")
	code, out := do(t, h, "GET", "/recipes/search?q=x", nil)
	if code != 200 {
		t.Fatal(code)
	}
	recs := out["recipes"].([]any)
	chili, salad := recs[0].(map[string]any), recs[1].(map[string]any)
	if chili["kcal_source"] != "mealie" || chili["kcal_per_serving"].(float64) != 520 || chili["protein_per_serving"].(float64) != 35 {
		t.Errorf("chili %v", chili)
	}
	if salad["kcal_source"] != "missing" || salad["ingredients"] == nil {
		t.Errorf("salad %v", salad)
	}
}

func TestTodayUsesHouseholdTimeZone(t *testing.T) {
	f := newFake()
	f.plans = []mealie.MealPlanEntry{{ID: 1, Date: "2026-11-30", EntryType: "dinner", Title: "UTC day"}, {ID: 2, Date: "2026-12-01", EntryType: "dinner", Title: "Warsaw day"}}
	h := newTestServer(t, f, "")
	_, out := do(t, h, "GET", "/mealplan/today", nil)
	if out["date"] != "2026-12-01" || len(out["entries"].([]any)) != 1 {
		t.Errorf("date = %v", out["date"])
	}
}

func TestSaveRequiresConfirm(t *testing.T) {
	f := newFake()
	h := newTestServer(t, f, "")
	code, _ := do(t, h, "POST", "/mealplan", map[string]any{"entries": []any{map[string]any{"date": "2026-12-01", "meal": "dinner", "recipe_slug": "chili"}}})
	if code != http.StatusPreconditionRequired || len(f.plans) != 0 {
		t.Errorf("code=%d plans=%d", code, len(f.plans))
	}
}

func TestSaveConflictWritesNothing(t *testing.T) {
	f := newFake()
	f.plans = []mealie.MealPlanEntry{{ID: 7, Date: "2026-12-01", EntryType: "dinner", Title: "Pizza (added by hand)"}}
	h := newTestServer(t, f, "")
	body := map[string]any{"confirm": true, "entries": []any{
		map[string]any{"date": "2026-12-01", "meal": "lunch", "recipe_slug": "chili"},
		map[string]any{"date": "2026-12-01", "meal": "dinner", "recipe_slug": "chili"},
	}}
	code, out := do(t, h, "POST", "/mealplan", body)
	if code != http.StatusConflict {
		t.Fatalf("code=%d", code)
	}
	if len(f.plans) != 1 || len(out["conflicts"].([]any)) != 1 {
		t.Errorf("plans=%d conflicts=%v", len(f.plans), out["conflicts"])
	}

	body["replace_existing"] = true
	code, out = do(t, h, "POST", "/mealplan", body)
	if code != 200 {
		t.Fatalf("replace code=%d %v", code, out)
	}
	if len(f.deleted) != 1 || f.deleted[0] != 7 || len(f.plans) != 2 {
		t.Errorf("deleted=%v plans=%v", f.deleted, f.plans)
	}
	if f.plans[0].RecipeID != "uuid-chili" {
		t.Errorf("recipe id not resolved: %+v", f.plans[0])
	}
}

func TestSaveUnknownRecipeWritesNothing(t *testing.T) {
	f := newFake()
	h := newTestServer(t, f, "")
	code, _ := do(t, h, "POST", "/mealplan", map[string]any{"confirm": true, "entries": []any{
		map[string]any{"date": "2026-12-01", "meal": "lunch", "recipe_slug": "chili"},
		map[string]any{"date": "2026-12-02", "meal": "lunch", "recipe_slug": "nope"},
	}})
	if code != http.StatusBadRequest || len(f.plans) != 0 {
		t.Errorf("code=%d plans=%d", code, len(f.plans))
	}
}

func TestPortionsEndpoint(t *testing.T) {
	h := newTestServer(t, newFake(), "")
	code, out := do(t, h, "POST", "/portions", map[string]any{"meal": "dinner", "kcal_per_serving": 520, "protein_per_serving": 35})
	if code != 200 || out["base_servings"].(float64) != 1.25 {
		t.Errorf("code=%d out=%v", code, out)
	}
	code, _ = do(t, h, "POST", "/portions", map[string]any{"meal": "dinner", "kcal_per_serving": 500, "target_kcal": 1500})
	if code != http.StatusBadRequest {
		t.Errorf("whole-day meal should be rejected, got %d", code)
	}
}

func TestImportAndShoppingList(t *testing.T) {
	f := newFake()
	h := newTestServer(t, f, "")
	if code, _ := do(t, h, "POST", "/recipes/import", map[string]any{"confirm": true, "url": "javascript:alert(1)"}); code != http.StatusBadRequest {
		t.Errorf("bad url accepted: %d", code)
	}
	code, out := do(t, h, "POST", "/recipes/import", map[string]any{"confirm": true, "url": "https://example.com/chili"})
	if code != 200 || out["saved"] != true {
		t.Errorf("import code=%d %v", code, out)
	}
	code, _ = do(t, h, "POST", "/shopping-list", map[string]any{"confirm": true, "items": []string{"600 g kurczaka", "2 cukinie"}})
	if code != 200 || len(f.lists["Nanny Ogg — 2026-12-01"]) != 2 {
		t.Errorf("shopping code=%d lists=%v", code, f.lists)
	}
}
