package getters

func NewRandomYear(name string, format int, allowNull bool) *RandomIntRange {
	if format == 2 {
		return NewRandomIntRange(name, 01, 99, allowNull)
	}
	return NewRandomIntRange(name, 1901, 2155, allowNull)
}

func NewRandomYearRange(name string, min, max int64, allowNull bool) *RandomIntRange {
	return NewRandomIntRange(name, min, max, allowNull)
}
