package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
)

// runSmokeTest exercises the core service and the HTTP layer end-to-end
// without any external dependency. It returns a non-nil error (causing exit 1)
// on the first failed assertion.
func runSmokeTest() error {
	if err := smokeCore(); err != nil {
		return fmt.Errorf("core: %w", err)
	}
	if err := smokeHTTP(); err != nil {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

func smokeCore() error {
	// Scenario 1: empty ring must refuse to map a key.
	s := NewService()
	if _, err := s.Owner("anything"); err == nil {
		return errors.New("mapping a key on an empty ring should fail")
	}

	// Scenario 2: add two nodes, every key must map to one of them, and the
	// mapping must be deterministic.
	if _, err := s.AddNode("alpha", 0); err != nil {
		return fmt.Errorf("add alpha: %w", err)
	}
	if _, err := s.AddNode("beta", 0); err != nil {
		return fmt.Errorf("add beta: %w", err)
	}
	key := "user-42"
	owner1, err := s.Owner(key)
	if err != nil {
		return fmt.Errorf("owner after add: %w", err)
	}
	if owner1 != "alpha" && owner1 != "beta" {
		return fmt.Errorf("owner = %q, want alpha or beta", owner1)
	}
	owner2, err := s.Owner(key)
	if err != nil {
		return fmt.Errorf("owner second call: %w", err)
	}
	if owner2 != owner1 {
		return fmt.Errorf("non-deterministic owner: %q then %q", owner1, owner2)
	}

	// Scenario 3: minimal migration. Removing a node must not change the owner
	// of any key that was NOT owned by the removed node. Keys owned by the
	// removed node move to its clockwise successor (which, with one node left,
	// is the only other node).
	keys := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		keys = append(keys, fmt.Sprintf("k-%d", i))
	}
	before, err := s.Owners(keys)
	if err != nil {
		return fmt.Errorf("owners before: %w", err)
	}
	// Choose the node to remove as whichever owns fewer of these keys, so the
	// other node certainly owns keys that must stay put.
	alphaCount, betaCount := 0, 0
	for _, o := range before {
		switch o {
		case "alpha":
			alphaCount++
		case "beta":
			betaCount++
		}
	}
	var remove, keep string
	if alphaCount <= betaCount {
		remove, keep = "alpha", "beta"
	} else {
		remove, keep = "beta", "alpha"
	}
	if err := s.RemoveNode(remove); err != nil {
		return fmt.Errorf("remove %s: %w", remove, err)
	}
	after, err := s.Owners(keys)
	if err != nil {
		return fmt.Errorf("owners after: %w", err)
	}
	for _, k := range keys {
		b := before[k]
		a := after[k]
		if b == keep && a != keep {
			return fmt.Errorf("minimal migration violated: key %q was on %q (not removed) but moved to %q", k, keep, a)
		}
		if b == remove && a != keep {
			return fmt.Errorf("key %q owned by removed %q should move to %q, got %q", k, remove, keep, a)
		}
	}

	// Scenario 4: with a single remaining node, every key maps to it.
	for _, k := range keys {
		o, err := s.Owner(k)
		if err != nil {
			return fmt.Errorf("owner single-node: %w", err)
		}
		if o != keep {
			return fmt.Errorf("single-node owner = %q, want %q", o, keep)
		}
	}

	// Scenario 5: removing the last node returns the ring to empty.
	if err := s.RemoveNode(keep); err != nil {
		return fmt.Errorf("remove %s: %w", keep, err)
	}
	if _, err := s.Owner(key); err == nil {
		return errors.New("mapping a key after removing all nodes should fail")
	}

	// Scenario 6: validation errors.
	s2 := NewService()
	if _, err := s2.AddNode("   ", 0); err == nil {
		return errors.New("blank node name should be rejected")
	}
	if _, err := s2.AddNode("a/b", 0); err == nil {
		return errors.New("node name containing '/' should be rejected")
	}
	if _, err := s2.AddNode("good", -1); err == nil {
		return errors.New("negative virtualNodes should be rejected")
	}
	if _, err := s2.AddNode("good", 0); err != nil {
		return fmt.Errorf("add good: %w", err)
	}
	if _, err := s2.AddNode("good", 0); err == nil {
		return errors.New("duplicate node name should be rejected")
	}
	if err := s2.RemoveNode("nope"); err == nil {
		return errors.New("removing a missing node should fail")
	}
	if _, err := s2.GetNode("nope"); err == nil {
		return errors.New("getting a missing node should fail")
	}

	// Scenario 7: list order follows join order.
	s3 := NewService()
	if _, err := s3.AddNode("n3", 0); err != nil {
		return fmt.Errorf("add n3: %w", err)
	}
	if _, err := s3.AddNode("n1", 0); err != nil {
		return fmt.Errorf("add n1: %w", err)
	}
	if _, err := s3.AddNode("n2", 0); err != nil {
		return fmt.Errorf("add n2: %w", err)
	}
	got := nodeNames(s3.ListNodes())
	if want := []string{"n3", "n1", "n2"}; !equalSlices(got, want) {
		return fmt.Errorf("list order = %v, want %v", got, want)
	}

	return nil
}

func smokeHTTP() error {
	srv := NewService()
	ts := httptest.NewServer(buildMux(srv))
	defer ts.Close()

	// healthz
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// empty-ring owner must be 400
	resp, err = http.Get(ts.URL + "/owner?key=x")
	if err != nil {
		return fmt.Errorf("get owner empty: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("empty-ring owner status = %d, want 400", resp.StatusCode)
	}

	// add a node via POST /nodes
	resp, err = http.Post(ts.URL+"/nodes", "application/json", bytes.NewBufferString(`{"name":"alpha"}`))
	if err != nil {
		return fmt.Errorf("post nodes: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("post nodes status = %d, want 201", resp.StatusCode)
	}

	// owner now resolves
	resp, err = http.Get(ts.URL + "/owner?key=user-1")
	if err != nil {
		return fmt.Errorf("get owner: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("owner status = %d, want 200", resp.StatusCode)
	}
	var res struct {
		Key   string `json:"key"`
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("decode owner: %w", err)
	}
	if res.Owner != "alpha" {
		return fmt.Errorf("owner = %q, want alpha", res.Owner)
	}

	// batch owners
	resp, err = http.Post(ts.URL+"/owners", "application/json", bytes.NewBufferString(`{"keys":["a","b","c"]}`))
	if err != nil {
		return fmt.Errorf("post owners: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("owners status = %d, want 200", resp.StatusCode)
	}
	var batch struct {
		Owners map[string]string `json:"owners"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&batch); err != nil {
		return fmt.Errorf("decode owners: %w", err)
	}
	if len(batch.Owners) != 3 {
		return fmt.Errorf("owners count = %d, want 3", len(batch.Owners))
	}

	// invalid JSON
	resp, err = http.Post(ts.URL+"/nodes", "application/json", bytes.NewBufferString(`{bad`))
	if err != nil {
		return fmt.Errorf("post bad json: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("bad json status = %d, want 400", resp.StatusCode)
	}

	// negative virtualNodes
	resp, err = http.Post(ts.URL+"/nodes", "application/json", bytes.NewBufferString(`{"name":"x","virtualNodes":-5}`))
	if err != nil {
		return fmt.Errorf("post bad vn: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("bad vn status = %d, want 400", resp.StatusCode)
	}

	// delete a missing node -> 404
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/nodes/missing", nil)
	if err != nil {
		return fmt.Errorf("new delete request: %w", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete missing: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete missing status = %d, want 404", resp.StatusCode)
	}

	return nil
}

func nodeNames(ns []*Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
