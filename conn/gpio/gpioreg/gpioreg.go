// Copyright 2017 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package gpioreg defines a registry for the known digital pins.
package gpioreg

import (
	"errors"
	"strconv"
	"sync"

	"periph.io/x/periph/conn/gpio"
)

// ByName returns a GPIO pin from its name, gpio number or one of its aliases.
//
// For example on a Raspberry Pi, the following values will return the same
// GPIO: the gpio as a number "2", the chipset name "GPIO2", the board pin
// position "P1_3", it's function name "I2C1_SDA".
//
// Returns nil if the gpio pin is not present.
func ByName(name string) gpio.PinIO {
	mu.Lock()
	defer mu.Unlock()
	return getByName(name)
}

// All returns all the GPIO pins available on this host.
//
// The list is guaranteed to be in order of number.
//
// This list excludes aliases.
//
// This list excludes non-GPIO pins like GROUND, V3_3, etc, since they are not
// GPIO.
func All() []gpio.PinIO {
	mu.Lock()
	defer mu.Unlock()
	out := make([]gpio.PinIO, 0, len(byNumber))
	seen := make(map[int]struct{}, len(byNumber[0]))
	// Memory-mapped pins have highest priority, include all of them.
	for _, p := range byNumber[0] {
		out = insertPinByNumber(out, p)
		seen[p.Number()] = struct{}{}
	}
	// Add in OS accessible pins that cannot be accessed via memory-map.
	for _, p := range byNumber[1] {
		if _, ok := seen[p.Number()]; !ok {
			out = insertPinByNumber(out, p)
		}
	}
	return out
}

// Aliases returns all pin aliases.
//
// The list is guaranteed to be in order of aliase name.
func Aliases() []gpio.PinIO {
	mu.Lock()
	defer mu.Unlock()
	out := make([]gpio.PinIO, 0, len(byAlias))
	for _, p := range byAlias {
		// Skip aliases that were not resolved. This requires resolving all aliases.
		if p.PinIO == nil {
			if p.PinIO = getByName(p.dest); p.PinIO == nil {
				continue
			}
		}
		out = insertPinByName(out, p)
	}
	return out
}

// Register registers a GPIO pin.
//
// Registering the same pin number or name twice is an error.
//
// `preferred` should be true when the pin being registered is exposing as much
// functionality as possible via the underlying hardware. This is normally done
// by accessing the CPU memory mapped registers directly.
//
// `preferred` should be false when the functionality is provided by the OS and
// is limited or slower.
//
// The pin registered cannot implement the interface RealPin.
func Register(p gpio.PinIO, preferred bool) error {
	name := p.Name()
	if len(name) == 0 {
		return errors.New("gpioreg: can't register a pin with no name")
	}
	if _, err := strconv.Atoi(name); err == nil {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + " with name being only a number")
	}
	number := p.Number()
	if number < 0 {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + " with invalid pin number " + strconv.Itoa(number))
	}
	i := 0
	other := 1
	if !preferred {
		i = 1
		other = 0
	}

	mu.Lock()
	defer mu.Unlock()
	if orig, ok := byNumber[i][number]; ok {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + " twice with the same number " + strconv.Itoa(number) + "; already registered as " + strconv.Quote(orig.String()))
	}
	if orig, ok := byName[i][name]; ok {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + " twice; already registered as " + strconv.Quote(orig.String()))
	}
	if r, ok := p.(gpio.RealPin); ok {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + ", it is already an alias: " + strconv.Quote(r.Real().String()) + "; use RegisterAlias() instead")
	}
	if alias, ok := byAlias[name]; ok {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + "; an alias already exist: " + strconv.Quote(alias.String()))
	}
	if orig, ok := byName[other][name]; ok && number != orig.Number() {
		return errors.New("gpioreg: can't register pin " + strconv.Quote(name) + " twice with different number; already registered as " + strconv.Quote(orig.String()))
	}
	byNumber[i][number] = p
	byName[i][name] = p
	return nil
}

// RegisterAlias registers an alias for a GPIO pin.
//
// It is possible to register an alias for a pin that itself has not been
// registered yet. It is valid to register an alias to another alias or to a
// number. It is valid to register the same alias to the same dest multiple
// times.
func RegisterAlias(alias string, dest string) error {
	if len(alias) == 0 {
		return errors.New("gpioreg: can't register an alias with no name")
	}
	if len(dest) == 0 {
		return errors.New("gpioreg: can't register alias " + strconv.Quote(alias) + " with no dest")
	}
	if _, err := strconv.Atoi(alias); err == nil {
		return errors.New("gpioreg: can't register alias " + strconv.Quote(alias) + " with name being only a number")
	}

	mu.Lock()
	defer mu.Unlock()
	if orig := byAlias[alias]; orig != nil {
		if orig.dest == dest {
			// It is fine to register the same alias twice. This simplifies unit
			// tests as there is no way to clear the registry (yet).
			return nil
		}
		return errors.New("gpioreg: can't register alias " + strconv.Quote(alias) + " twice; it is already an alias: " + strconv.Quote(orig.String()))
	}
	byAlias[alias] = &pinAlias{name: alias, dest: dest}
	return nil
}

//

var (
	mu sync.Mutex
	// The first map is preferred pins, the second is for more limited pins,
	// usually going through OS-provided abstraction layer.
	byNumber = [2]map[int]gpio.PinIO{{}, {}}
	byName   = [2]map[string]gpio.PinIO{{}, {}}
	byAlias  = map[string]*pinAlias{}
)

// pinAlias implements an alias for a PinIO.
//
// pinAlias implements the RealPin interface, which allows querying for the
// real pin under the alias.
type pinAlias struct {
	gpio.PinIO
	name string
	dest string
}

// String returns the alias name along the real pin's Name() in parenthesis, if
// known, else the real pin's number.
func (a *pinAlias) String() string {
	if a.PinIO == nil {
		return a.name + "(" + a.dest + ")"
	}
	return a.name + "(" + a.PinIO.Name() + ")"
}

// Name returns the pinAlias's name.
func (a *pinAlias) Name() string {
	return a.name
}

// Real returns the real pin behind the alias
func (a *pinAlias) Real() gpio.PinIO {
	return a.PinIO
}

func getByNumber(number int) gpio.PinIO {
	if p, ok := byNumber[0][number]; ok {
		return p
	}
	if p, ok := byNumber[1][number]; ok {
		return p
	}
	return nil
}

// getByName recursively resolves the aliases to get the pin.
func getByName(name string) gpio.PinIO {
	if p, ok := byName[0][name]; ok {
		return p
	}
	if p, ok := byName[1][name]; ok {
		return p
	}
	if p, ok := byAlias[name]; ok {
		if p.PinIO == nil {
			if p.PinIO = getByName(p.dest); p.PinIO == nil {
				return nil
			}
		}
		return p
	}
	if i, err := strconv.Atoi(name); err == nil {
		return getByNumber(i)
	}
	return nil
}

func insertPinByNumber(l []gpio.PinIO, p gpio.PinIO) []gpio.PinIO {
	n := p.Number()
	i := search(len(l), func(i int) bool { return l[i].Number() > n })
	l = append(l, nil)
	copy(l[i+1:], l[i:])
	l[i] = p
	return l
}

func insertPinByName(l []gpio.PinIO, p gpio.PinIO) []gpio.PinIO {
	n := p.Name()
	i := search(len(l), func(i int) bool { return l[i].Name() > n })
	l = append(l, nil)
	copy(l[i+1:], l[i:])
	l[i] = p
	return l
}

// search implements the same algorithm as sort.Search().
//
// It was extracted to to not depend on sort, which depends on reflect.
func search(n int, f func(int) bool) int {
	lo := 0
	for hi := n; lo < hi; {
		if i := int(uint(lo+hi) >> 1); !f(i) {
			lo = i + 1
		} else {
			hi = i
		}
	}
	return lo
}
