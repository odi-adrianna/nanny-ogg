// Package plan holds the deterministic meal-planning rules from docs/spec.md:
// the household profile, per-meal calorie targets, portion scaling between the
// two people, meal-prep / freezing splits and the daily protein check. The LLM
// decides what to cook; the numbers come from here so they are the same every
// time.
package plan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

//go:embed profile.json
var defaultProfile []byte

type Person struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	DailyKcal         float64 `json:"daily_kcal"`
	BreakfastKcal     float64 `json:"breakfast_kcal"`
	BreakfastProteinG float64 `json:"breakfast_protein_g"`
	WeightKg          float64 `json:"weight_kg"`
	ProteinMinG       float64 `json:"protein_min_g"`
	ProteinMaxG       float64 `json:"protein_max_g"`
	PortionMultiplier float64 `json:"portion_multiplier"`
}

type MealRange struct {
	MinKcal float64 `json:"min_kcal"`
	MaxKcal float64 `json:"max_kcal"`
}

type Profile struct {
	People              []Person             `json:"people"`
	Meals               map[string]MealRange `json:"meals"`
	MinMealProteinShare float64              `json:"min_meal_protein_share"`
}

// Load reads a profile from path, or the embedded default when path is empty.
func Load(path string) (*Profile, error) {
	data := defaultProfile
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data = b
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	if len(p.People) == 0 {
		return nil, errors.New("profile has no people")
	}
	if _, ok := p.base(); !ok {
		return nil, errors.New("profile needs one person with portion_multiplier 1.0")
	}
	return &p, nil
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("ł", "l", "ń", "n", "ó", "o", "ś", "s", "ą", "a", "ę", "e", "ż", "z", "ź", "z", "ć", "c").Replace(s)
}

// Person finds a person by id or name, ignoring case and Polish diacritics.
func (p *Profile) Person(idOrName string) (Person, bool) {
	k := normalize(idOrName)
	for _, pp := range p.People {
		if normalize(pp.ID) == k || normalize(pp.Name) == k {
			return pp, true
		}
	}
	return Person{}, false
}

// base is the person whose portion is 1.0× (all recipe math starts there).
func (p *Profile) base() (Person, bool) {
	for _, pp := range p.People {
		if pp.PortionMultiplier == 1 {
			return pp, true
		}
	}
	return Person{}, false
}

func (p *Profile) resolvePeople(ids []string) ([]Person, error) {
	if len(ids) == 0 {
		return p.People, nil
	}
	out := []Person{}
	seen := map[string]bool{}
	for _, id := range ids {
		pp, ok := p.Person(id)
		if !ok {
			return nil, fmt.Errorf("unknown person %q", id)
		}
		if !seen[pp.ID] {
			seen[pp.ID] = true
			out = append(out, pp)
		}
	}
	return out, nil
}

// round rounds v to the nearest step, then trims float noise (0.7000000001).
func round(v float64, step float64) float64 {
	r := math.Round(v/step) * step
	return math.Round(r*1e6) / 1e6
}

// ---------------------------------------------------------------------------
// Portions

type PortionsRequest struct {
	Meal              string   `json:"meal"`                // lunch | dinner
	KcalPerServing    float64  `json:"kcal_per_serving"`    // 0 = no kcal this time
	ProteinPerServing float64  `json:"protein_per_serving"` // 0 = unknown
	KcalEstimated     bool     `json:"kcal_estimated"`      // kcal is an estimate, not from the recipe
	TargetKcal        float64  `json:"target_kcal"`         // override for the 1.0× person; 0 = default
	Swap              bool     `json:"swap"`                // use the other meal's default range
	People            []string `json:"people"`              // default: everyone
	Days              int      `json:"days"`                // meal-prep days, default 1
	Freeze            bool     `json:"freeze"`              // batch cooking with freezing
	RecipeServings    float64  `json:"recipe_servings"`     // servings the recipe makes as written
	CookedWeightG     float64  `json:"cooked_weight_g"`     // whole cooked dish, for splitting by weight
}

type PersonPortion struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Servings       float64 `json:"servings"`  // recipe servings per meal
	Kcal           float64 `json:"kcal"`      // per meal
	ProteinG       float64 `json:"protein_g"` // per meal, 0 if unknown
	SharePct       float64 `json:"share_pct"` // share of each day's pot
	FreshPortions  int     `json:"fresh_portions"`
	FrozenPortions int     `json:"frozen_portions"`
	BoxWeightG     float64 `json:"box_weight_g,omitempty"` // grams per box when cooked weight given
	ProteinLow     bool    `json:"protein_low"`
}

type PortionsResult struct {
	Meal            string          `json:"meal"`
	KcalSource      string          `json:"kcal_source"`   // recipe | estimated | none
	TargetKcal      float64         `json:"target_kcal"`   // for the 1.0× person
	BaseServings    float64         `json:"base_servings"` // servings for the 1.0× person; pass to day-check
	People          []PersonPortion `json:"people"`
	Days            int             `json:"days"`
	TotalServings   float64         `json:"total_servings"`             // to cook, in recipe servings
	IngredientScale float64         `json:"ingredient_scale,omitempty"` // multiply recipe ingredients by this
	Notes           []string        `json:"notes"`
}

func otherMeal(m string) string {
	if m == "lunch" {
		return "dinner"
	}
	return "lunch"
}

// Portions scales one dish for the people eating it.
func (p *Profile) Portions(req PortionsRequest) (*PortionsResult, error) {
	meal := strings.ToLower(strings.TrimSpace(req.Meal))
	if _, ok := p.Meals[meal]; !ok {
		return nil, fmt.Errorf("meal must be lunch or dinner, got %q", req.Meal)
	}
	if req.KcalPerServing < 0 || req.ProteinPerServing < 0 || req.TargetKcal < 0 || req.Days < 0 || req.CookedWeightG < 0 {
		return nil, errors.New("numbers must not be negative")
	}
	people, err := p.resolvePeople(req.People)
	if err != nil {
		return nil, err
	}
	base, _ := p.base()
	days := req.Days
	if days == 0 {
		days = 1
	}

	res := &PortionsResult{Meal: meal, Days: days, Notes: []string{}}

	rangeMeal := meal
	if req.Swap {
		rangeMeal = otherMeal(meal)
	}
	rng := p.Meals[rangeMeal]
	target := req.TargetKcal
	if target == 0 {
		target = (rng.MinKcal + rng.MaxKcal) / 2
	}
	// A single meal may never take the whole day: leave room for the other
	// main meal's minimum after the fixed breakfast.
	maxMeal := base.DailyKcal - base.BreakfastKcal - p.Meals[otherMeal(meal)].MinKcal
	if target > maxMeal {
		return nil, fmt.Errorf("target %.0f kcal is too much for one meal: with breakfast and the other meal it would exceed %s's day (max %.0f)", target, base.Name, maxMeal)
	}
	res.TargetKcal = target

	baseServings := 1.0
	switch {
	case req.KcalPerServing == 0:
		res.KcalSource = "none"
		res.Notes = append(res.Notes, "no kcal: portions are 1 serving for the 1.0× person, scaled by multiplier")
	default:
		res.KcalSource = "recipe"
		if req.KcalEstimated {
			res.KcalSource = "estimated"
			res.Notes = append(res.Notes, "kcal is an ESTIMATE from the ingredients — say so to the user")
		}
		baseServings = round(target/req.KcalPerServing, 0.05)
		if baseServings < 0.05 {
			baseServings = 0.05
		}
	}

	res.BaseServings = baseServings
	var perDay float64
	for _, pp := range people {
		perDay += baseServings * pp.PortionMultiplier
	}
	for _, pp := range people {
		s := round(baseServings*pp.PortionMultiplier, 0.01)
		pr := PersonPortion{
			ID:       pp.ID,
			Name:     pp.Name,
			Servings: s,
			Kcal:     math.Round(s * req.KcalPerServing),
			ProteinG: round(s*req.ProteinPerServing, 0.5),
			SharePct: round(100*baseServings*pp.PortionMultiplier/perDay, 0.1),
		}
		if req.Freeze {
			pr.FreshPortions, pr.FrozenPortions = 1, days-1
		} else {
			pr.FreshPortions = days
		}
		if req.CookedWeightG > 0 {
			pr.BoxWeightG = math.Round(req.CookedWeightG * baseServings * pp.PortionMultiplier / (perDay * float64(days)))
		}
		if req.ProteinPerServing > 0 && pr.ProteinG < p.MinMealProteinShare*pp.ProteinMinG {
			pr.ProteinLow = true
			res.Notes = append(res.Notes, fmt.Sprintf("%s's portion has only %.0f g protein (want ≥ %.0f g per main meal) — suggest a protein add-on", pp.Name, pr.ProteinG, p.MinMealProteinShare*pp.ProteinMinG))
		}
		res.People = append(res.People, pr)
	}
	if req.ProteinPerServing == 0 {
		res.Notes = append(res.Notes, "protein per serving unknown — estimate it from the ingredients and label it as an estimate")
	}
	if req.Freeze {
		res.Notes = append(res.Notes, "freezing: each person eats 1 portion fresh, the rest goes to the freezer")
	}
	if req.Freeze && days < 2 {
		res.Notes = append(res.Notes, "nothing to freeze with 1 day — at least one portion per person always stays fresh")
	}
	res.TotalServings = round(perDay*float64(days), 0.05)
	if req.RecipeServings > 0 {
		res.IngredientScale = round(res.TotalServings/req.RecipeServings, 0.05)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Day check

type DayMeal struct {
	Meal              string  `json:"meal"`
	KcalPerServing    float64 `json:"kcal_per_serving"`
	ProteinPerServing float64 `json:"protein_per_serving"`
	BaseServings      float64 `json:"base_servings"` // servings for the 1.0× person (from portions)
}

type DayRequest struct {
	People []string  `json:"people"`
	Meals  []DayMeal `json:"meals"`
}

type AddOn struct {
	Item     string  `json:"item"`
	Amount   string  `json:"amount"`
	ProteinG float64 `json:"protein_g"`
	Kcal     float64 `json:"kcal"`
}

type PersonDay struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kcal         float64 `json:"kcal"`
	BufferKcal   float64 `json:"buffer_kcal"` // left for snacks/coffee — shown, not planned
	ProteinG     float64 `json:"protein_g"`
	ProteinMinG  float64 `json:"protein_min_g"`
	ProteinMaxG  float64 `json:"protein_max_g"`
	ProteinGapG  float64 `json:"protein_gap_g"`
	AddOnOptions []AddOn `json:"add_on_options,omitempty"` // each option alone closes the gap
}

type DayResult struct {
	People []PersonDay `json:"people"`
	Notes  []string    `json:"notes"`
}

// protein sources for add-on suggestions: protein and kcal per unit.
var addOnSources = []struct {
	item      string
	unit      string
	perUnit   float64 // unit size (g or pieces)
	proteinPU float64
	kcalPU    float64
}{
	{"cooked chicken breast", "g", 100, 31, 165},
	{"skyr", "g", 100, 11, 63},
	{"eggs", "pcs", 1, 6.5, 75},
	{"cottage cheese", "g", 100, 11, 98},
}

// Day totals one day for each person: breakfast (fixed) + planned meals,
// the snack buffer and the protein gap with add-on options.
func (p *Profile) Day(req DayRequest) (*DayResult, error) {
	people, err := p.resolvePeople(req.People)
	if err != nil {
		return nil, err
	}
	res := &DayResult{Notes: []string{}}
	for _, pp := range people {
		d := PersonDay{
			ID: pp.ID, Name: pp.Name,
			Kcal:        pp.BreakfastKcal,
			ProteinG:    pp.BreakfastProteinG,
			ProteinMinG: pp.ProteinMinG,
			ProteinMaxG: pp.ProteinMaxG,
		}
		for _, m := range req.Meals {
			s := m.BaseServings
			if s == 0 {
				s = 1
			}
			s *= pp.PortionMultiplier
			d.Kcal += s * m.KcalPerServing
			d.ProteinG += s * m.ProteinPerServing
		}
		d.Kcal = math.Round(d.Kcal)
		d.ProteinG = round(d.ProteinG, 0.5)
		d.BufferKcal = pp.DailyKcal - d.Kcal
		if d.BufferKcal < 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("%s is %.0f kcal over the day — make a portion smaller", pp.Name, -d.BufferKcal))
		}
		if gap := pp.ProteinMinG - d.ProteinG; gap > 0 {
			d.ProteinGapG = round(gap, 0.5)
			for _, src := range addOnSources {
				units := math.Ceil(gap / src.proteinPU * src.perUnit)
				if src.unit == "g" {
					units = math.Ceil(units/10) * 10
				}
				amount := fmt.Sprintf("%.0f %s", units, src.unit)
				d.AddOnOptions = append(d.AddOnOptions, AddOn{
					Item:     src.item,
					Amount:   amount,
					ProteinG: round(units/src.perUnit*src.proteinPU, 0.5),
					Kcal:     math.Round(units / src.perUnit * src.kcalPU),
				})
			}
			res.Notes = append(res.Notes, fmt.Sprintf("%s is %.0f g short of protein — suggest ONE add-on just for them (add its kcal to the day)", pp.Name, d.ProteinGapG))
		}
		res.People = append(res.People, d)
	}
	return res, nil
}
