package main

import (
	"fmt"
	"strings"
)

func prependZerosHex(s string, length int) string {
	for len(s) < length {
		s = "0" + s
	}
	return s
}

func padHexString(dataHex string, mSize int) (string, bool) {
	if strings.HasPrefix(dataHex, "0x") {
		dataHex = dataHex[2:]
	}

	bug := false

	words := []string{}

	for i := 0; i < len(dataHex); i += 64 {
		end := i + 64
		if end > len(dataHex) {
			end = len(dataHex)
		}
		word := dataHex[i:end]
		words = append(words, word)
	}

	lastWordIndex := len(words) - 1
	lastWord := words[lastWordIndex]

	if len(lastWord) > 0 && lastWord[0] == '0' && lastWord[1] != '0' {
		tmpLastWord := lastWord[1:]
		if len(tmpLastWord) < mSize*2 {
			bug = true
			lastWord = tmpLastWord + "0"
		}
	}
	words[lastWordIndex] = lastWord

	dataHex = strings.Join(words, "")

	return "0x" + dataHex, bug
}

func main() {
	type testScenario struct {
		hexString string
		mSize     int
		bug       bool
	}

	scenarios := map[int]testScenario{
		1: {hexString: "0x010203", mSize: 3, bug: true},
		2: {hexString: "0x0102030405060708090a0b0c0d0e0f0102030405060708090a0b0c0d0e0f0102030405060708090a0b0c0d0e0f0102030405060708090a0b0c0d0e0f", mSize: 64, bug: true},
		3: {hexString: "0x11", mSize: 1, bug: false},
		4: {hexString: "0x00000000000000000000000000000000000000000000000000000000000001ff", mSize: 32, bug: false},
		5: {hexString: "0x00000000000000000000000000000000000000000000000000005af3107a4000", mSize: 32, bug: false},
		6: {hexString: "0x060303e606c27f9cddd90a7f129f525c83a0be7108fd5209174a77ffa7809e1c", mSize: 32, bug: false},
		7: {hexString: "0000000000000000000000000000000000000000000000000000000000009ce10000000000000000000000005071d96f0251884debe6f2e02fa610df183859e3000000000000000000000000000000000000000000000000000000000000000200000000000000000000000061d79bc5dc25e6c4aee44b34cfcdfb47f0d984100d1dcde1acde7f8bf8e5c1a9f9a3f2394500fa3fcf2620acee012d87fa860745",
			mSize: 160,
			bug:   false,
		},
	}

	for i := 1; i <= len(scenarios); i++ {
		fmt.Println("Scenario", i)
		paddedHexString, bug := padHexString(scenarios[i].hexString, scenarios[i].mSize)
		if bug != scenarios[i].bug {
			fmt.Printf("FAIL! Expected bug: %t, got: %t\n", scenarios[i].bug, bug)
		}
		fmt.Printf("Original Hex: %s\n", scenarios[i].hexString)
		fmt.Printf("Padded Hex:   %s\n", paddedHexString)
		fmt.Println("")
	}
}
