# nanny-ogg

Tool server for **Nanny Ogg**, the meal-planning assistant for Adrianna and Paweł. It exposes the household profile, portion and protein math, the Mealie recipe book and the Mealie meal-plan calendar as an OpenAPI tool server for [Open WebUI](https://github.com/open-webui/open-webui). Models are served through the cluster's LiteLLM gateway. The layout follows [mended-drum](https://github.com/thaum-xyz/mended-drum).

- Business rules: [`docs/spec.md`](docs/spec.md)
- System prompt for the Open WebUI model: [`prompts/system.md`](prompts/system.md)
- Household numbers (kcal, breakfasts, protein targets, multipliers): [`internal/plan/profile.json`](internal/plan/profile.json). This file is embedded in the build. Override it at runtime with `PROFILE_FILE`.

## v1 scope

- Mealie only. Web recipe search, and the "proven: ≥ 4.5★ and 100+ ratings" rule from the spec, are deferred. A recipe link the user pastes is imported through Mealie's scraper.
- No database. Mealie is the source of truth, and the spec rules out an inventory between weeks.

## Tools

| Endpoint | Purpose |
|---|---|
| `GET /profile` | profile and today's date (in `TZ`, default Europe/Warsaw) |
| `GET /recipes/search`, `GET /recipes/{slug}` | Mealie recipes with kcal/protein per serving and `kcal_source` |
| `POST /recipes/import` | save a recipe from a URL into Mealie (`confirm`) |
| `POST /portions` | per-person servings / kcal / protein, ×1.2 for Paweł, meal-prep totals, grams per box, fresh vs frozen |
| `POST /day-check` | daily totals, snack buffer, protein gap and add-on options |
| `GET /mealplan`, `GET /mealplan/today` | read the calendar |
| `POST /mealplan` | write entries (`confirm`); returns 409 with conflicts and writes nothing if a slot is taken, unless `replace_existing` is set |
| `POST /mealplan/delete` | remove entries (`confirm`) |
| `POST /shopping-list` | create a Mealie shopping list (`confirm`) |

Every write needs `confirm: true`. Tool endpoints require `Authorization: Bearer $TOOLS_API_KEY` when `TOOLS_API_KEY` is set.

## Configuration

| Env | |
|---|---|
| `MEALIE_BASE_URL` | e.g. `http://mealie.mealie.svc:9000` in-cluster, `https://recipes.krupa.net.pl` locally |
| `MEALIE_API_TOKEN` | Mealie → Profile → API Tokens (required) |
| `TOOLS_API_KEY` | optional bearer key for the tool endpoints |
| `TZ` | household time zone, default `Europe/Warsaw` |
| `PROFILE_FILE` | optional path to a profile JSON |
| `PORT` | default `8080` |

## Local development

```sh
cp .env.example .env   # fill in MEALIE_API_TOKEN; .env is git-ignored
make run               # serves on :8080
curl localhost:8080/healthz
make test
```
