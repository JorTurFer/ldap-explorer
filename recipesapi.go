package main

// Ready-made queries (see internal/recipes), adapted to the connected server.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/JorTurFer/ldap-explorer/internal/recipes"
)

func recipeEnv() (*recipes.Env, error) {
	c, _, err := sess.get()
	if err != nil {
		return nil, err
	}
	env := &recipes.Env{Now: time.Now(), Attr: func(string) bool { return false }, Class: func(string) bool { return false }}
	if _, attrs, classes, err := getSchemaFull(c); err == nil {
		env.HasSchema = true
		env.Attr = func(n string) bool { _, ok := attrs[strings.ToLower(n)]; return ok }
		env.Class = func(n string) bool { return classes[strings.ToLower(n)] }
	}
	return env, nil
}

func handleRecipes(w http.ResponseWriter, _ *http.Request) {
	env, err := recipeEnv()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, recipes.List(env))
}

func handleRecipe(w http.ResponseWriter, r *http.Request) {
	env, err := recipeEnv()
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		Params map[string]string `json:"params"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	plan, err := recipes.Apply(env, r.PathValue("id"), req.Params)
	if errors.Is(err, recipes.ErrUnknown) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, plan)
}
