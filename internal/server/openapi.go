package server

// openAPISpec is the OpenAPI 3.1 document Open WebUI fetches to register the
// meal-planning tools. Descriptions are written for the LLM that selects the
// tools.
const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "Nanny Ogg meal-planning tools",
    "version": "0.1.0",
    "description": "Tools for planning lunches and dinners for Adrianna and Paweł: the household profile (calories, protein targets, portion multipliers), the recipe book and the meal-plan calendar in Mealie (the source of truth for what is planned), portion and protein calculations, and saving shopping lists."
  },
  "servers": [{"url": "http://nanny-ogg-tools.nanny-ogg.svc:8080"}],
  "paths": {
    "/profile": {
      "get": {
        "operationId": "getProfile",
        "summary": "Household profile and today's date",
        "description": "Returns both people's daily kcal, fixed breakfast kcal/protein, daily protein targets, portion multipliers (Adrianna 1.0, Paweł 1.2), default lunch/dinner kcal ranges, plus today's date and weekday in the household time zone. Call this at the start of a conversation and whenever you need dates.",
        "responses": {"200": {"description": "Profile", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/recipes/search": {
      "get": {
        "operationId": "searchRecipes",
        "summary": "Search recipes in Mealie",
        "description": "Search the household's own recipe book in Mealie. Always check here first. Each result has servings, times, tags, ingredients, kcal and protein per serving when Mealie has them. kcal_source='missing' means Mealie has no kcal: estimate kcal and protein per serving from the ingredients yourself and label them as estimates — never skip a recipe just because kcal is missing. Search with short keywords (an ingredient, a dish, a cuisine), not whole sentences. The book mixes Polish and English recipes and matching is literal, so search every keyword in BOTH languages (e.g. 'ciecierzyca' and 'chickpeas') and merge the results.",
        "parameters": [
          {"name": "q", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Keyword, e.g. 'kurczak', 'curry', 'makaron'. Omit to list recipes."},
          {"name": "max", "in": "query", "required": false, "schema": {"type": "integer", "default": 10}, "description": "Maximum results (capped at 25)"}
        ],
        "responses": {"200": {"description": "Matching recipes", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/recipes/{slug}": {
      "get": {
        "operationId": "getRecipe",
        "summary": "Get a full recipe from Mealie",
        "description": "Full recipe for a slug: ingredients, steps, servings, kcal/protein per serving (if known) and source link.",
        "parameters": [{"name": "slug", "in": "path", "required": true, "schema": {"type": "string"}, "description": "Recipe slug from a search result"}],
        "responses": {"200": {"description": "Recipe", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/recipes/import": {
      "post": {
        "operationId": "importRecipeFromUrl",
        "summary": "Save a recipe from a web link into Mealie",
        "description": "When the user pastes a link to a recipe page, Mealie scrapes and saves it. Set confirm=true only after the user agrees to save it. Returns the saved recipe (use its slug for the meal plan).",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["confirm", "url"],
          "properties": {
            "confirm": {"type": "boolean", "description": "Must be true; set only after the user approves saving"},
            "url": {"type": "string", "description": "Full http(s) link to the recipe page"}
          }
        }}}},
        "responses": {"200": {"description": "Saved recipe", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/portions": {
      "post": {
        "operationId": "calculatePortions",
        "summary": "Calculate portions for one dish",
        "description": "Use this for EVERY proposed lunch/dinner instead of doing the math yourself. Gives each person's servings, kcal and protein per meal (Paweł = 1.2× Adrianna), the share of each day's pot (for splitting by weight), how many servings to cook in total, how much to scale the recipe, fresh vs frozen portions, and grams per box if the cooked weight is known. Leave target_kcal empty for the default range midpoint; pass it when the user gives a number (e.g. 'dinner max 600'). Pass kcal_estimated=true when you estimated the kcal. Pass kcal_per_serving=0 when the user said no kcal this time. Read the notes in the response and pass them on.",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["meal"],
          "properties": {
            "meal": {"type": "string", "enum": ["lunch", "dinner"]},
            "kcal_per_serving": {"type": "number", "description": "Recipe kcal per serving (from Mealie or your estimate); 0 = no kcal this time"},
            "protein_per_serving": {"type": "number", "description": "Recipe protein (g) per serving; 0 = unknown"},
            "kcal_estimated": {"type": "boolean", "description": "True if kcal/protein are your estimate"},
            "target_kcal": {"type": "number", "description": "Adrianna's target kcal for this meal, only if the user gave one"},
            "swap": {"type": "boolean", "description": "True when lunch and dinner sizes are swapped today (lunch gets the dinner range and vice versa)"},
            "people": {"type": "array", "items": {"type": "string"}, "description": "Who eats it, e.g. ['adrianna'] or ['pawel']; omit for both"},
            "days": {"type": "integer", "description": "Meal-prep days this batch covers; default 1"},
            "freeze": {"type": "boolean", "description": "Batch cooking with freezing (each person keeps 1 portion fresh, the rest is frozen)"},
            "recipe_servings": {"type": "number", "description": "Servings the recipe makes as written (for the ingredient scale)"},
            "cooked_weight_g": {"type": "number", "description": "Weight of the whole cooked dish in grams, if the user weighed it"}
          }
        }}}},
        "responses": {"200": {"description": "Portions", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/day-check": {
      "post": {
        "operationId": "checkDay",
        "summary": "Check a day's kcal and protein for each person",
        "description": "Totals one day per person: fixed breakfast + the planned lunch and dinner. Returns total kcal, the snack/coffee buffer (show it, don't plan it), protein vs the daily target and, when someone is short, add-on options (each one alone closes the gap). Use base_servings from calculatePortions. Call once per planned day.",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["meals"],
          "properties": {
            "people": {"type": "array", "items": {"type": "string"}, "description": "Who eats that day; omit for both"},
            "meals": {"type": "array", "items": {"type": "object", "properties": {
              "meal": {"type": "string", "enum": ["lunch", "dinner"]},
              "kcal_per_serving": {"type": "number"},
              "protein_per_serving": {"type": "number"},
              "base_servings": {"type": "number", "description": "Adrianna's servings from calculatePortions (Paweł is scaled automatically)"}
            }}}
          }
        }}}},
        "responses": {"200": {"description": "Day totals", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/mealplan": {
      "get": {
        "operationId": "getMealPlan",
        "summary": "Read the Mealie meal-plan calendar",
        "description": "Entries between start and end (inclusive, YYYY-MM-DD). Mealie is the source of truth for what is planned — always read it here, never answer from memory. Read the days you are about to plan BEFORE proposing, so you can tell the user what is already there.",
        "parameters": [
          {"name": "start", "in": "query", "required": false, "schema": {"type": "string"}, "description": "YYYY-MM-DD; default today"},
          {"name": "end", "in": "query", "required": false, "schema": {"type": "string"}, "description": "YYYY-MM-DD; default = start"}
        ],
        "responses": {"200": {"description": "Entries", "content": {"application/json": {"schema": {"type": "object"}}}}}
      },
      "post": {
        "operationId": "saveMealPlan",
        "summary": "Write the approved plan to the Mealie calendar",
        "description": "Writes entries to the Mealie calendar. Call ONLY after the user explicitly approves the final plan, with confirm=true. Use recipe_slug for Mealie recipes (import web links first), and put portions and storage notes in note, e.g. 'Adrianna 1.25 porcji (~650 kcal, 44 g białka) · Paweł 1.5 · sos osobno'. If a slot already has an entry the call returns 409 with the conflicts and writes NOTHING: show them to the user and ask; retry with replace_existing=true only if they agree to replace, otherwise drop those entries and retry.",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["confirm", "entries"],
          "properties": {
            "confirm": {"type": "boolean", "description": "Must be true; set only after explicit approval"},
            "replace_existing": {"type": "boolean", "description": "Replace entries already in those slots; only after the user agreed"},
            "entries": {"type": "array", "items": {"type": "object", "required": ["date", "meal"], "properties": {
              "date": {"type": "string", "description": "YYYY-MM-DD"},
              "meal": {"type": "string", "enum": ["lunch", "dinner", "breakfast", "side", "snack"]},
              "recipe_slug": {"type": "string", "description": "Mealie recipe slug"},
              "title": {"type": "string", "description": "Title for entries without a Mealie recipe, e.g. 'Leftovers: chili'"},
              "note": {"type": "string", "description": "Portions per person, who eats it, storage / fresh-sauce notes"}
            }}}
          }
        }}}},
        "responses": {"200": {"description": "Saved", "content": {"application/json": {"schema": {"type": "object"}}}}, "409": {"description": "Slots already taken; nothing written"}}
      }
    },
    "/mealplan/today": {
      "get": {
        "operationId": "getTodayMealPlan",
        "summary": "What's planned for today",
        "description": "Today's entries from the Mealie calendar. Use for 'what are we eating today?'. Each entry's note holds the portions per person; always explain them to the user (Mealie's calendar does not show notes on recipe entries).",
        "responses": {"200": {"description": "Today's entries", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/mealplan/delete": {
      "post": {
        "operationId": "deleteMealPlanEntries",
        "summary": "Remove entries from the Mealie calendar",
        "description": "Removes calendar entries by id (ids come from getMealPlan). Used for replanning, e.g. moving a dish that wasn't cooked: delete the old entry and save the new one. confirm=true only after the user approves.",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["confirm", "ids"],
          "properties": {
            "confirm": {"type": "boolean"},
            "ids": {"type": "array", "items": {"type": "integer"}}
          }
        }}}},
        "responses": {"200": {"description": "Deleted", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/shopping-list": {
      "post": {
        "operationId": "saveShoppingList",
        "summary": "Save a shopping list to Mealie",
        "description": "Creates a new shopping list in Mealie with one line per item (e.g. '600 g pierś z kurczaka'). Leave out what the user said is already at home. confirm=true only when the user wants it saved.",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["confirm", "items"],
          "properties": {
            "confirm": {"type": "boolean"},
            "name": {"type": "string", "description": "List name; default 'Nanny Ogg — <date>'"},
            "items": {"type": "array", "items": {"type": "string"}}
          }
        }}}},
        "responses": {"200": {"description": "Saved", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`
