package core

type Style string

const (
	Plain Style = ""
	Bold  Style = "bold"
	Code  Style = "code"
)

type Span struct {
	Text  string
	Style Style
}

type Text []Span

func Literal(text string) Text {
	return Text{{Text: text}}
}

type Button struct {
	Caption string
	Data    string
}

type View struct {
	Text     Text
	Rows     [][]Button
	Menu     string
	Feedback string
}
