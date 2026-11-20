package main

// POST /api/filter turns the query builder's tree of conditions into an LDAP
// filter (see internal/ldapfilter), validating it against the server's schema.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/JorTurFer/ldap-explorer/internal/ldapfilter"
)

type filterRequest struct {
	Query *ldapfilter.Node `json:"query"`
}

type filterResponse struct {
	Filter          string `json:"filter"`
	Explain         string `json:"explain"`
	SchemaAvailable bool   `json:"schemaAvailable"` // false: the server hides its schema, so attributes and operators were not checked
}

type filterError struct {
	Error  string             `json:"error"`
	Errors []ldapfilter.Error `json:"errors"`
}

func handleFilter(w http.ResponseWriter, r *http.Request) {
	c, _, err := sess.get()
	if err != nil {
		writeErr(w, err)
		return
	}
	var req filterRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	opt := ldapfilter.Options{}
	_, idx, schemaErr := getSchema(c)
	if schemaErr == nil {
		opt.Lookup = func(attr string) (ldapfilter.AttrInfo, bool) {
			info, ok := idx[strings.ToLower(attr)]
			return info, ok
		}
	}
	res, errs := ldapfilter.Build(req.Query, opt)
	if len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(filterError{Error: errs[0].Error(), Errors: errs})
		return
	}
	writeJSON(w, filterResponse{Filter: res.Filter, Explain: res.Explain, SchemaAvailable: schemaErr == nil})
}
