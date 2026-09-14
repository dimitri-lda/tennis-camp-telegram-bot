package main

import "testing"

func TestRequiresManager(t *testing.T) {
	tests := []struct {
		question string
		want     bool
	}{
		{question: "Есть ли свободные места?", want: true},
		{question: "Можно оплатить депозит?", want: true},
		{question: "Сколько часов тенниса в день?", want: false},
	}

	for _, test := range tests {
		if got := requiresManager(test.question); got != test.want {
			t.Errorf("requiresManager(%q) = %t, want %t", test.question, got, test.want)
		}
	}
}
