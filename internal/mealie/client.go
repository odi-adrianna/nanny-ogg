// Package mealie is a small client for the Mealie (v3) REST API: recipes,
// nutrition, the household meal-plan calendar and shopping lists.
package mealie

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for the Mealie instance at baseURL, authenticating with
// a long-lived API token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// FlexString decodes a JSON string, number or null into a string. Mealie's
// nutrition and yield fields have been both strings and numbers across versions.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null" || s == "":
		*f = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = FlexString(v)
	default:
		*f = FlexString(s)
	}
	return nil
}

var firstNumber = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)?`)

// Number extracts the first number in the value ("450 kcal" -> 450). ok is
// false when there is no number or it is zero (Mealie stores "" or "0" for
// "unknown").
func (f FlexString) Number() (float64, bool) {
	m := firstNumber.FindString(string(f))
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", "."), 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

type Tag struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

type Food struct {
	Name string `json:"name"`
}

type Unit struct {
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation"`
}

type Ingredient struct {
	Quantity float64 `json:"quantity"`
	Unit     *Unit   `json:"unit"`
	Food     *Food   `json:"food"`
	Note     string  `json:"note"`
	Display  string  `json:"display"`
}

// Text returns the human-readable ingredient line.
func (i Ingredient) Text() string {
	if d := strings.TrimSpace(i.Display); d != "" {
		return d
	}
	parts := []string{}
	if i.Quantity > 0 {
		parts = append(parts, strconv.FormatFloat(i.Quantity, 'f', -1, 64))
	}
	if i.Unit != nil && i.Unit.Name != "" {
		parts = append(parts, i.Unit.Name)
	}
	if i.Food != nil && i.Food.Name != "" {
		parts = append(parts, i.Food.Name)
	}
	if n := strings.TrimSpace(i.Note); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, " ")
}

type Instruction struct {
	Text string `json:"text"`
}

// Nutrition is per serving, as Mealie stores it.
type Nutrition struct {
	Calories       FlexString `json:"calories"`
	ProteinContent FlexString `json:"proteinContent"`
	CarbContent    FlexString `json:"carbohydrateContent"`
	FatContent     FlexString `json:"fatContent"`
}

type Recipe struct {
	ID                 string        `json:"id"`
	Slug               string        `json:"slug"`
	Name               string        `json:"name"`
	Description        string        `json:"description"`
	OrgURL             string        `json:"orgURL"`
	Tags               []Tag         `json:"tags"`
	RecipeCategory     []Tag         `json:"recipeCategory"`
	RecipeServings     float64       `json:"recipeServings"`
	RecipeYield        FlexString    `json:"recipeYield"`
	TotalTime          FlexString    `json:"totalTime"`
	PrepTime           FlexString    `json:"prepTime"`
	PerformTime        FlexString    `json:"performTime"`
	Rating             float64       `json:"rating"`
	Nutrition          *Nutrition    `json:"nutrition"`
	RecipeIngredient   []Ingredient  `json:"recipeIngredient"`
	RecipeInstructions []Instruction `json:"recipeInstructions"`
}

// Servings returns the number of servings the recipe makes, falling back to the
// yield text, or 0 when unknown.
func (r Recipe) Servings() float64 {
	if r.RecipeServings > 0 {
		return r.RecipeServings
	}
	if v, ok := r.RecipeYield.Number(); ok {
		return v
	}
	return 0
}

type Summary struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tags        []Tag  `json:"tags"`
}

// MealPlanEntry is one entry in the household meal-plan calendar.
type MealPlanEntry struct {
	ID        int      `json:"id,omitempty"`
	Date      string   `json:"date"`
	EntryType string   `json:"entryType"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	RecipeID  string   `json:"recipeId,omitempty"`
	Recipe    *Summary `json:"recipe,omitempty"`
	UserID    string   `json:"userId,omitempty"`
}

// APIError is a non-2xx response from Mealie.
type APIError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mealie %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodGet, path, nil, out)
}

// SearchRecipes returns up to perPage recipe summaries matching query.
func (c *Client) SearchRecipes(ctx context.Context, query string, perPage int) ([]Summary, error) {
	q := url.Values{}
	if query != "" {
		q.Set("search", query)
	}
	q.Set("perPage", strconv.Itoa(perPage))
	q.Set("page", "1")
	var resp struct {
		Items []Summary `json:"items"`
	}
	if err := c.get(ctx, "/api/recipes?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// GetRecipe returns the full recipe for a slug (or id).
func (c *Client) GetRecipe(ctx context.Context, slug string) (*Recipe, error) {
	var r Recipe
	if err := c.get(ctx, "/api/recipes/"+url.PathEscape(slug), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ImportFromURL asks Mealie to scrape a recipe page and save it; returns the
// new recipe's slug.
func (c *Client) ImportFromURL(ctx context.Context, recipeURL string) (string, error) {
	var slug string
	body := map[string]any{"url": recipeURL, "includeTags": false}
	if err := c.request(ctx, http.MethodPost, "/api/recipes/create/url", body, &slug); err != nil {
		return "", err
	}
	return slug, nil
}

// ListMealPlans returns calendar entries between start and end (inclusive,
// YYYY-MM-DD).
func (c *Client) ListMealPlans(ctx context.Context, start, end string) ([]MealPlanEntry, error) {
	q := url.Values{}
	q.Set("start_date", start)
	q.Set("end_date", end)
	q.Set("perPage", "-1")
	q.Set("page", "1")
	q.Set("orderBy", "date")
	q.Set("orderDirection", "asc")
	var resp struct {
		Items []MealPlanEntry `json:"items"`
	}
	if err := c.get(ctx, "/api/households/mealplans?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// CreateMealPlanEntry adds one entry to the calendar.
func (c *Client) CreateMealPlanEntry(ctx context.Context, e MealPlanEntry) (*MealPlanEntry, error) {
	body := map[string]any{
		"date":      e.Date,
		"entryType": e.EntryType,
		"title":     e.Title,
		"text":      e.Text,
	}
	if e.RecipeID != "" {
		body["recipeId"] = e.RecipeID
	}
	var out MealPlanEntry
	if err := c.request(ctx, http.MethodPost, "/api/households/mealplans", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMealPlanEntry removes one calendar entry.
func (c *Client) DeleteMealPlanEntry(ctx context.Context, id int) error {
	return c.request(ctx, http.MethodDelete, "/api/households/mealplans/"+strconv.Itoa(id), nil, nil)
}

// CreateShoppingList creates a list with one free-text item per line and
// returns the list id.
func (c *Client) CreateShoppingList(ctx context.Context, name string, items []string) (string, error) {
	var list struct {
		ID string `json:"id"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/households/shopping/lists", map[string]string{"name": name}, &list); err != nil {
		return "", err
	}
	payload := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			payload = append(payload, map[string]any{"shoppingListId": list.ID, "note": it, "quantity": 1, "isFood": false})
		}
	}
	if len(payload) > 0 {
		if err := c.request(ctx, http.MethodPost, "/api/households/shopping/items/create-bulk", payload, nil); err != nil {
			return list.ID, err
		}
	}
	return list.ID, nil
}
