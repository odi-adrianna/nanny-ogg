package mealie

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlexStringNumber(t *testing.T) {
	cases := map[string]float64{`"450 kcal"`: 450, `"12,5 g"`: 12.5, `520`: 520, `null`: 0, `""`: 0, `"0"`: 0}
	for in, want := range cases {
		var f FlexString
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		got, ok := f.Number()
		if got != want || ok != (want > 0) {
			t.Errorf("%s -> %v, %v; want %v", in, got, ok, want)
		}
	}
}

func TestMealPlanRequests(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/households/mealplans":
			if r.URL.Query().Get("start_date") != "2026-12-01" || r.URL.Query().Get("end_date") != "2026-12-03" {
				t.Errorf("query %v", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":5,"date":"2026-12-01","entryType":"dinner","title":"","text":"","recipe":{"slug":"chili","name":"Chili"}}]}`))
		case r.Method == "POST" && r.URL.Path == "/api/households/mealplans":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["recipeId"] != "uuid" || body["entryType"] != "lunch" {
				t.Errorf("body %v", body)
			}
			_, _ = w.Write([]byte(`{"id":6,"date":"2026-12-02","entryType":"lunch"}`))
		case r.Method == "DELETE" && r.URL.Path == "/api/households/mealplans/5":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == "GET" && r.URL.Path == "/api/recipes/chili":
			_, _ = w.Write([]byte(`{"id":"uuid","slug":"chili","recipeServings":0,"recipeYield":"4 porcje","nutrition":{"calories":"520","proteinContent":null}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "tok")
	ctx := context.Background()
	items, err := c.ListMealPlans(ctx, "2026-12-01", "2026-12-03")
	if err != nil || len(items) != 1 || items[0].Recipe.Slug != "chili" {
		t.Fatalf("list: %v %v", items, err)
	}
	if _, err := c.CreateMealPlanEntry(ctx, MealPlanEntry{Date: "2026-12-02", EntryType: "lunch", RecipeID: "uuid"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMealPlanEntry(ctx, 5); err != nil {
		t.Fatal(err)
	}
	rec, err := c.GetRecipe(ctx, "chili")
	if err != nil || rec.Servings() != 4 {
		t.Fatalf("recipe servings: %v %v", rec, err)
	}
	if _, err := c.GetRecipe(ctx, "missing"); err == nil {
		t.Error("expected 404 error")
	} else if ae, ok := err.(*APIError); !ok || ae.Status != 404 {
		t.Errorf("err %v", err)
	}
}
