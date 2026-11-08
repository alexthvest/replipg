package core

type Style string

const (
	Plain  Style = ""
	Bold   Style = "bold"
	Code   Style = "code"
	Italic Style = "italic"
)

type Span struct {
	Text  string
	Style Style
}

type Text []Span

func Literal(text string) Text {
	return Text{{Text: text}}
}

type ButtonStyle string

const (
	ButtonDefault ButtonStyle = ""
	ButtonPrimary ButtonStyle = "primary"
	ButtonSuccess ButtonStyle = "success"
	ButtonDanger  ButtonStyle = "danger"
)

type Button struct {
	Caption string
	Data    string
	Style   ButtonStyle
}

type View struct {
	Text     Text
	Rows     [][]Button
	Menu     string
	Feedback string
}
