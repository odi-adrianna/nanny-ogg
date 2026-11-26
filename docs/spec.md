# Meal Planning Agent — Concept & Requirements

## Goal

The agent takes manual meal planning off Adrianna's plate: coming up with dishes, spreading them across days, keeping track of freshness/spoilage, using ingredients already at home, and splitting calories between two people with different needs.

Priorities, in order:
1. **Tasty** — taste wins over strict macros.
2. **Enough protein** — the one nutritional rule that is actively tracked (see *Protein*).
3. **Healthy** — nice to have.

Adrianna's earlier nutrition plan (`nutrition-plan.md`) is **not** binding here — only the calorie target is taken from it. Its macro targets do not apply.

## Scope & users

- A **personal tool** for two people: Adrianna and Paweł. Not a product; no onboarding or multi-tenant concerns.
- **Adrianna** is the main user. **Paweł** occasionally talks to the agent too and has **the same rights**: he can ask questions, report changes and write to Mealie.
- Both eat **the same meals** — one menu, different portion sizes. Paweł has no separate likes/dislikes to take into account.
- Main focus: **lunches and dinners**. Breakfast is routine and only counted as a fixed calorie value.
- Lunches are often meal-prepped for several days — but not always.

## Profile (single source of truth for all numbers)

| | Adrianna | Paweł |
|---|---|---|
| Daily calories | 1750 kcal | 2100 kcal |
| Breakfast (fixed) | ~450 kcal | ~550 kcal |
| Body weight | 58 kg | 92 kg |
| Daily protein target (1.2–1.5 g/kg, more is fine) | 70–87 g | 110–138 g |
| Portion multiplier | 1.0× | 1.2× |

The 1.2× multiplier matches the calorie ratio exactly (2100 / 1750 = 1.2). All recipe calculations are done for Adrianna's portion first; Paweł's portion is that × 1.2.

## Calories & portions

Default daily split (Adrianna):

| Meal | kcal |
|---|---|
| Breakfast | ~450 (fixed) |
| Lunch | 400–500 |
| Dinner | 600–700 |
| Snacks / coffee buffer | the rest (~100–300) |

- Lunch and dinner values are sometimes swapped.
- The default can be overridden per request with a specific number (e.g. "dinner max 600 kcal").
- Neither lunch nor dinner may ever take 100% of the daily budget — calories are always spread out.
- The remaining buffer is for snacks, coffee etc. The agent shows it but does **not** plan it.

## Protein

- Target is **daily**, by body weight: 1.2–1.5 g/kg (more is fine) → Adrianna 70–87 g, Paweł 110–138 g.
- Every lunch and dinner should carry a meaningful share of protein. If a dish falls short, the agent suggests a protein add-on (yogurt, egg, legumes, larger meat portion, etc.) rather than rejecting the dish.
- ⚠️ **Known tension:** Paweł's portion is 1.2× Adrianna's, but his protein target is ~1.6× hers (92 / 58 kg). Scaling the same dish will not get him there on its own. The agent should suggest an **extra protein component just for Paweł** (e.g. extra chicken breast, side of skyr) when his daily total falls below target.

## Two modes

### 1. Weekly plan

- The agent opens with a general question for the whole week: anything you're craving, anything to avoid, any theme.
- It asks about the "vibe": type of cuisine, how much time for cooking, how many days to plan.
- For lunches it explicitly asks for the **meal-prep horizon** (how many days) before proposing anything.
- Recipe sources — always in parallel:
  - recipes already in **Mealie**,
  - recipes from the **web**, only well-rated, "proven" ones (many ratings).
- Adrianna (or Paweł) chooses.

### 2. Ad hoc request

- Pattern: *"I feel like something like this, I have this much time, more protein-focused — what do you suggest?"*
- The preferences question is asked every time, not just once a week.
- The "proven / highly rated only" rule does **not** apply — speed and inspiration matter.
- Mealie is still checked first, but without the rigour of the weekly mode.

## Ingredients already at home

- Reported verbally, on the fly ("I have chicken, zucchini, yogurt is running out").
- No separate, persistent inventory.

## Meal prep & storage

- Output adapted to how long a given dish keeps.
- Sensitive components (sauces, dressings) are listed separately so they can be added fresh on the day of eating.
- Weekend batch cooking with freezing: at least one portion per person must always be left (you can't freeze "down to zero").

## Plan flexibility

- A specific day can be skipped (e.g. going out, eating out).
- A day can be planned for only one person (e.g. the other is away / eating elsewhere).

## Final output

- Concrete recipes (from Mealie and/or the web) laid out by day, with kcal and protein per portion for each person.
- A **shopping list** that accounts for what's already at home.
- After confirmation — automatic save of the chosen recipes to Mealie and assignment to days in the **Mealie meal plan calendar**.

## Tracking during the week

- Questions like "what's planned for today?" — the agent checks the Mealie calendar directly. **Mealie is the source of truth** for the plan, not a separate list.

## Replanning

- When something falls through (e.g. a planned dish wasn't cooked), Adrianna or Paweł reports it — the agent does not monitor proactively.
- The agent moves unused ingredients to the remaining days of the week instead of letting them "disappear" from the plan.

## Technical setup

- **Mealie:** self-hosted, reachable from the internet.
- **Agent:** lives in a dedicated repository (Claude Code).
- **Auth:** a Mealie API token (to be generated by Adrianna). Keep it in an environment variable / `.env` that is **not committed** to the repo.
- Since both users can write to Mealie, the agent should show the existing calendar entry before replacing it (see *Known gaps*).

## Open questions (not yet decided)

- **Integration method:** a script in the repo calling the Mealie API, or an existing Mealie MCP server?
- **"Proven" web recipe threshold:** e.g. ≥ 4.5★ and ≥ 100 ratings? Which sites / languages? How many options per slot (proposal: 3)?
- **Portioning in meal prep:** e.g. lunch for 3 days for two = 3 × (1 + 1.2) = 6.6 of Adrianna's portions. Options: scale raw ingredients and split by eye / weigh the cooked dish and split by percentage (proposal) / weigh only protein and carbs.
- **Freezing rule:** does "at least one portion per person" mean one portion each is eaten fresh and the rest frozen? Should the agent remember what's in the freezer across weeks (conflicts with "no inventory between weeks")?
- **Breakfast protein:** how much protein the fixed breakfasts provide — needed to know how much lunch + dinner must cover.

## Known gaps (consciously deferred — "we'll see in practice")

- Reliability of kcal data in web recipes (source errors can throw off portion scaling).
- No ingredient memory between weeks — anything not reported in week 1 effectively disappears from the system in week 2.
- Ad hoc sync with the Mealie calendar — does cooking something other than planned overwrite the Mealie entry?
- Risk of overwriting during automatic saves to Mealie (e.g. an entry added manually, or by the other person).
