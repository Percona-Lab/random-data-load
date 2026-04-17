package generate

type DefaultKeyword struct {
}

func (r *DefaultKeyword) String() string {
	return "DEFAULT"
}

func (r *DefaultKeyword) IsQuotable() bool {
	return false
}

func NewDefaultKeyword() *DefaultKeyword {
	return &DefaultKeyword{}
}
