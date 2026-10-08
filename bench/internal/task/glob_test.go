package task

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// go
		{"**/*_test.go", "x_test.go", true},
		{"**/*_test.go", "a/b/x_test.go", true},
		{"**/*_test.go", "queue/retry_test.go", true},
		{"**/*_test.go", "x_test.gox", false},
		{"**/*_test.go", "queue/retry.go", false},
		{"**/*_test.go", "xtest.go", false},

		// python
		{"**/test_*.py", "test_cart.py", true},
		{"**/test_*.py", "shop/test_cart.py", true},
		{"**/test_*.py", "shop/cart.py", false},
		{"**/*_test.py", "shop/cart_test.py", true},
		{"tests/**", "tests/conftest.py", true},
		{"tests/**", "tests/a/b.py", true},
		{"tests/**", "src/tests/a.py", false},
		{"tests/**", "testsuite/a.py", false},

		// node
		{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "a.test.js", true},
		{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "src/ui/date.spec.tsx", true},
		{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "src/ui/date.test.mjs", true},
		{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "src/ui/date.test.py", false},
		{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "src/ui/date.js", false},
		{"**/__tests__/**", "__tests__/a.js", true},
		{"**/__tests__/**", "src/__tests__/a.js", true},
		{"**/__tests__/**", "src/__tests__/deep/a.js", true},
		{"**/__tests__/**", "src/tests/a.js", false},
		{"test/**", "test/a.js", true},

		// php
		{"tests/**/*Test.php", "tests/FooTest.php", true},
		{"tests/**/*Test.php", "tests/Unit/FooTest.php", true},
		{"tests/**/*Test.php", "tests/Unit/Foo.php", false},
		{"tests/**/*Test.php", "src/FooTest.php", false},

		// cpp
		{"**/*_test.{cc,cpp}", "a_test.cc", true},
		{"**/*_test.{cc,cpp}", "src/a_test.cpp", true},
		{"**/*_test.{cc,cpp}", "src/a_test.c", false},
		{"**/*_unittest.{cc,cpp}", "src/a_unittest.cc", true},

		// operators
		{"*.go", "a.go", true},
		{"*.go", "dir/a.go", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		{"a.go", "abgo", false},
		{"a+b", "a+b", true},
		{"**", "a/b/c", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/xb", false},
		{"{a,b/c}/d", "b/c/d", true},
		{"{a,{b,c}}x", "cx", true},
		{"a,b", "a,b", true},

		// malformed
		{"{a,b", "a", false},
		{"a}", "a}", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" "+tt.path, func(t *testing.T) {
			if got := Match(tt.pattern, tt.path); got != tt.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
