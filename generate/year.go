package generate

func NewRandomYear(format int) *RandomIntRange {
	if format == 2 {
		return NewRandomIntRange(01, 99)
	}
	return NewRandomIntRange(1901, 2155)
}

func NewRandomYearRange(min, max int64) *RandomIntRange {
	return NewRandomIntRange(min, max)
}
