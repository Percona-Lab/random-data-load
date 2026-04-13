package query

import (
	"reflect"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
)

type VirtualJoins []VirtualJoin

type VirtualJoin struct {
	Left  VirtualJoinPart
	Right VirtualJoinPart
}

type VirtualJoinPart struct {
	Table   string
	Columns []string
}

var ErrMalformedForeignKey = errors.New("malformed foreign-key, the format is 'parent_table.col1[,col2]=child_table.col1[,col2];other_parent.colx=other_child.coly'")

func (v VirtualJoins) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	err := ctx.Scan.PopValueInto("value", &value)
	if err != nil {
		return err
	}
	vfksRaw := strings.Split(value, ";")
	vfks := []VirtualJoin{}

	for _, vfkRaw := range vfksRaw {

		parts := strings.Split(vfkRaw, "=")
		if len(parts) != 2 {
			return ErrMalformedForeignKey
		}

		left := strings.Split(parts[0], ".")
		right := strings.Split(parts[1], ".")
		if len(left) != 2 || len(right) != 2 {
			return ErrMalformedForeignKey
		}

		parentTable := left[0]
		childTable := right[0]

		parentCols := strings.Split(left[1], ",")
		childCols := strings.Split(right[1], ",")
		if len(parentCols) != len(childCols) {
			return errors.Wrap(ErrMalformedForeignKey, "parent table and child table should have the same amount of columns")
		}

		vfk := VirtualJoin{
			Left: VirtualJoinPart{
				Table:   parentTable,
				Columns: parentCols,
			},
			Right: VirtualJoinPart{
				Table:   childTable,
				Columns: childCols,
			},
		}
		vfks = append(vfks, vfk)
	}
	target.Set(reflect.ValueOf(vfks))

	return nil
}
