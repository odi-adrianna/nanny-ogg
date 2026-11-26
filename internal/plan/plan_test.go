package plan

import (
	"strings"
	"testing"
)

func mustProfile(t *testing.T) *Profile {
	t.Helper()
	p, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPersonLookup(t *testing.T) {
	p := mustProfile(t)
	for _, in := range []string{"pawel", "Paweł", "PAWEŁ", " pawel "} {
		if pp, ok := p.Person(in); !ok || pp.ID != "pawel" {
			t.Errorf("Person(%q) = %v, %v", in, pp.ID, ok)
		}
	}
	if _, ok := p.Person("kasia"); ok {
		t.Error("unknown person found")
	}
}

func byID(res *PortionsResult, id string) PersonPortion {
	for _, pp := range res.People {
		if pp.ID == id {
			return pp
		}
	}
	return PersonPortion{}
}

func TestPortionsDefaultDinner(t *testing.T) {
	p := mustProfile(t)
	res, err := p.Portions(PortionsRequest{Meal: "dinner", KcalPerServing: 520, ProteinPerServing: 35, RecipeServings: 4})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetKcal != 650 || res.KcalSource != "recipe" {
		t.Fatalf("target=%v source=%v", res.TargetKcal, res.KcalSource)
	}
	a, pw := byID(res, "adrianna"), byID(res, "pawel")
	if a.Servings != 1.25 {
		t.Errorf("adrianna servings = %v, want 1.25", a.Servings)
	}
	if pw.Servings != 1.5 { // 1.25 * 1.2
		t.Errorf("pawel servings = %v, want 1.5", pw.Servings)
	}
	if a.Kcal != 650 || pw.Kcal != 780 {
		t.Errorf("kcal a=%v p=%v", a.Kcal, pw.Kcal)
	}
	if a.SharePct != 45.5 || pw.SharePct != 54.5 {
		t.Errorf("share a=%v p=%v", a.SharePct, pw.SharePct)
	}
	if res.TotalServings != 2.75 || res.IngredientScale != 0.7 {
		t.Errorf("total=%v scale=%v", res.TotalServings, res.IngredientScale)
	}
}

func TestPortionsOverrideAndSwap(t *testing.T) {
	p := mustProfile(t)
	res, err := p.Portions(PortionsRequest{Meal: "dinner", KcalPerServing: 600, TargetKcal: 600})
	if err != nil {
		t.Fatal(err)
	}
	if byID(res, "adrianna").Servings != 1 {
		t.Errorf("override: %v", byID(res, "adrianna").Servings)
	}
	res, err = p.Portions(PortionsRequest{Meal: "lunch", KcalPerServing: 650, Swap: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetKcal != 650 {
		t.Errorf("swap: lunch should use dinner range, got %v", res.TargetKcal)
	}
}

func TestPortionsNeverWholeDay(t *testing.T) {
	p := mustProfile(t)
	// 1750 - 450 breakfast - 400 lunch minimum = 900 max for dinner.
	if _, err := p.Portions(PortionsRequest{Meal: "dinner", KcalPerServing: 500, TargetKcal: 1200}); err == nil {
		t.Error("expected error for a meal taking the whole day")
	}
	if _, err := p.Portions(PortionsRequest{Meal: "dinner", KcalPerServing: 500, TargetKcal: 900}); err != nil {
		t.Errorf("900 should be allowed: %v", err)
	}
}

func TestPortionsMealPrepByWeight(t *testing.T) {
	p := mustProfile(t)
	// 3 days lunch for both: 3 * (1 + 1.2) = 6.6 base servings.
	res, err := p.Portions(PortionsRequest{Meal: "lunch", KcalPerServing: 450, Days: 3, CookedWeightG: 2640})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalServings != 6.6 {
		t.Errorf("total = %v, want 6.6", res.TotalServings)
	}
	a, pw := byID(res, "adrianna"), byID(res, "pawel")
	if a.BoxWeightG != 400 || pw.BoxWeightG != 480 {
		t.Errorf("boxes a=%v p=%v, want 400/480", a.BoxWeightG, pw.BoxWeightG)
	}
	if a.FreshPortions != 3 || a.FrozenPortions != 0 {
		t.Errorf("fresh/frozen = %v/%v", a.FreshPortions, a.FrozenPortions)
	}
}

func TestPortionsFreezeKeepsOneFresh(t *testing.T) {
	p := mustProfile(t)
	res, err := p.Portions(PortionsRequest{Meal: "dinner", KcalPerServing: 650, Days: 4, Freeze: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, pp := range res.People {
		if pp.FreshPortions != 1 || pp.FrozenPortions != 3 {
			t.Errorf("%s fresh/frozen = %v/%v, want 1/3", pp.ID, pp.FreshPortions, pp.FrozenPortions)
		}
	}
}

func TestPortionsOnePersonAndNoKcal(t *testing.T) {
	p := mustProfile(t)
	res, err := p.Portions(PortionsRequest{Meal: "dinner", People: []string{"Paweł"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.People) != 1 || res.People[0].ID != "pawel" || res.People[0].Servings != 1.2 || res.People[0].SharePct != 100 {
		t.Errorf("got %+v", res.People)
	}
	if res.KcalSource != "none" {
		t.Errorf("source = %v", res.KcalSource)
	}
}

func TestPortionsEstimatedAndLowProtein(t *testing.T) {
	p := mustProfile(t)
	res, err := p.Portions(PortionsRequest{Meal: "lunch", KcalPerServing: 450, ProteinPerServing: 10, KcalEstimated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.KcalSource != "estimated" {
		t.Errorf("source = %v", res.KcalSource)
	}
	if !byID(res, "adrianna").ProteinLow || !byID(res, "pawel").ProteinLow {
		t.Error("expected low protein for both")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "ESTIMATE") {
		t.Error("missing estimate note")
	}
}

func TestDayProteinGapForPawel(t *testing.T) {
	p := mustProfile(t)
	// Adrianna: 450 + 450 + 650 = 1550 kcal, 20 + 25 + 35 = 80 g protein.
	res, err := p.Day(DayRequest{Meals: []DayMeal{
		{Meal: "lunch", KcalPerServing: 450, ProteinPerServing: 25, BaseServings: 1},
		{Meal: "dinner", KcalPerServing: 650, ProteinPerServing: 35, BaseServings: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var a, pw PersonDay
	for _, d := range res.People {
		if d.ID == "adrianna" {
			a = d
		} else {
			pw = d
		}
	}
	if a.Kcal != 1550 || a.BufferKcal != 200 || a.ProteinG != 80 || a.ProteinGapG != 0 {
		t.Errorf("adrianna %+v", a)
	}
	// Paweł: 550 + 1.2*1100 = 1870 kcal; 20 + 1.2*60 = 92 g -> 18 g short of 110.
	if pw.Kcal != 1870 || pw.BufferKcal != 230 || pw.ProteinG != 92 || pw.ProteinGapG != 18 {
		t.Errorf("pawel %+v", pw)
	}
	if len(pw.AddOnOptions) == 0 || pw.AddOnOptions[0].ProteinG < 18 {
		t.Errorf("add-ons %+v", pw.AddOnOptions)
	}
}
