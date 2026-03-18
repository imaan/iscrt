// Copyright 2021-2023 Charles Francoise
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"sort"
	"testing"
)

func TestListPrefix(t *testing.T) {
	s := NewStore()
	s.Set("project-a/DB_HOST", []byte("localhost"))
	s.Set("project-a/API_KEY", []byte("sk-123"))
	s.Set("project-b/STRIPE", []byte("sk-456"))
	s.Set("no-prefix", []byte("value"))

	keys := s.ListPrefix("project-a/")
	sort.Strings(keys)

	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "project-a/API_KEY" || keys[1] != "project-a/DB_HOST" {
		t.Fatalf("unexpected keys: %v", keys)
	}

	// Empty prefix returns all
	all := s.ListPrefix("")
	if len(all) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(all))
	}

	// Non-matching prefix returns empty
	none := s.ListPrefix("nonexistent/")
	if len(none) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(none))
	}
}

func TestUnsetPrefix(t *testing.T) {
	s := NewStore()
	s.Set("project-a/DB_HOST", []byte("localhost"))
	s.Set("project-a/API_KEY", []byte("sk-123"))
	s.Set("project-b/STRIPE", []byte("sk-456"))

	count := s.UnsetPrefix("project-a/")
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}

	keys := s.List()
	if len(keys) != 1 || keys[0] != "project-b/STRIPE" {
		t.Fatalf("unexpected remaining keys: %v", keys)
	}
}
