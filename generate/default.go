package generate

type DefaultKeyword struct {
}

func (r *DefaultKeyword) Value() interface{} {
	return r.String()
}

func (r *DefaultKeyword) String() string {
	return "DEFAULT"
}

func (r *DefaultKeyword) Quote() string {
	return r.String()
}

func NewDefaultKeyword() *DefaultKeyword {
	return &DefaultKeyword{}
}
