// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"testing"
)

func TestEvalTrue(t *testing.T) {
	var tests = []struct {
		onsuccess bool
		onfailure bool
		expr      string
		data      map[string]interface{}
	}{
		{
			expr:      `branch == "main"`,
			data:      map[string]interface{}{"branch": "main"},
			onsuccess: true,
			onfailure: false,
		},
		{
			expr:      `branch != "main"`,
			data:      map[string]interface{}{"branch": "main"},
			onsuccess: false,
			onfailure: false,
		},
		{
			expr:      `pipeline.branch == "main"`, // field does not exist
			data:      map[string]interface{}{"pipeline": map[string]interface{}{}},
			onsuccess: false,
			onfailure: false,
		},
		{
			expr:      `branch != "main" || failure()`,
			data:      map[string]interface{}{"branch": "main"},
			onsuccess: false,
			onfailure: true,
		},
		{
			expr:      `always()`,
			data:      map[string]interface{}{"branch": "main"},
			onsuccess: true,
			onfailure: true,
		},
		{
			expr:      `failure()`,
			data:      map[string]interface{}{"branch": "main"},
			onsuccess: false,
			onfailure: true,
		},
	}

	for _, test := range tests {
		t.Run(test.expr, func(t *testing.T) {
			t.Log(test.expr)
			onsuccess, onfailure, err := evalif(test.expr, test.data)
			if err != nil {
				t.Error(err)
			}
			if got, want := onsuccess, test.onsuccess; got != want {
				t.Errorf("want on_success %v, got %v", want, got)
			}
			if got, want := onfailure, test.onfailure; got != want {
				t.Errorf("want on_failure %v, got %v", want, got)
			}
		})
	}
}
