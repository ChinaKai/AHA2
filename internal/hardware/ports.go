package hardware

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func sortSerialPorts(items []domain.SerialPort) {
	sort.Slice(items, func(i, j int) bool {
		leftPrefix, leftNumber := serialNameParts(items[i].Device)
		rightPrefix, rightNumber := serialNameParts(items[j].Device)
		if leftPrefix == rightPrefix && leftNumber >= 0 && rightNumber >= 0 {
			return leftNumber < rightNumber
		}
		return items[i].Device < items[j].Device
	})
}

func serialNameParts(value string) (string, int) {
	value = strings.TrimSpace(value)
	index := len(value)
	for index > 0 && value[index-1] >= '0' && value[index-1] <= '9' {
		index--
	}
	if index == len(value) {
		return value, -1
	}
	number, err := strconv.Atoi(value[index:])
	if err != nil {
		return value, -1
	}
	return value[:index], number
}
