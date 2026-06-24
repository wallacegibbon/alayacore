package terminal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// BenchmarkInputFieldInsert benchmarks inserting characters into InputField.
func BenchmarkInputFieldInsert(b *testing.B) {
	f := NewInputField()
	f.SetWidth(80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, _ = f.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	}
}

// BenchmarkInputFieldView benchmarks rendering the input field view.
func BenchmarkInputFieldView(b *testing.B) {
	f := NewInputField()
	f.SetWidth(80)
	f.Focus()

	for i := 0; i < 50; i++ {
		f, _ = f.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.View()
	}
}

// BenchmarkScrollViewSetContent benchmarks ScrollView.SetContent at various sizes.
func BenchmarkScrollViewSetContent(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	for _, n := range sizes {
		content := strings.Repeat("line of text for testing\n", n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			sv := NewScrollView(80, 40)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sv.SetContent(content)
			}
		})
	}
}

// BenchmarkScrollViewView benchmarks ScrollView.View at various sizes.
func BenchmarkScrollViewView(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	for _, n := range sizes {
		content := strings.Repeat("line of text for testing\n", n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			sv := NewScrollView(80, 40)
			sv.SetContent(content)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = sv.View()
			}
		})
	}
}

// BenchmarkScrollViewScroll benchmarks scrolling through content.
func BenchmarkScrollViewScroll(b *testing.B) {
	sv := NewScrollView(80, 40)
	sv.SetContent(strings.Repeat("line of text for testing\n", 1000))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sv.ScrollDown(1)
		if sv.AtBottom() {
			sv.GotoTop()
		}
	}
}
