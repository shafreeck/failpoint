// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

// Copyright 2016 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package failpoint

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrNotExist represents a failpoint can not be found by specified name
	ErrNotExist = fmt.Errorf("failpoint: failpoint does not exist")
	// ErrDisabled represents a failpoint is be disabled
	ErrDisabled = fmt.Errorf("failpoint: failpoint is disabled")
	// ErrNoContext returns by EvalContext when the context is nil
	ErrNoContext = fmt.Errorf("failpoint: no context")
	// ErrNoHook returns by EvalContext when there is no hook in the context
	ErrNoHook = fmt.Errorf("failpoint: no hook")
	// ErrFiltered represents a failpoint is filtered by a hook function
	ErrFiltered = fmt.Errorf("failpoint: filtered by hook")
)

func init() {
	failpoints.reg = make(map[string]*Failpoint)
	if s := os.Getenv("GO_FAILPOINTS"); len(s) > 0 {
		// format is <FAILPOINT>=<TERMS>[;<FAILPOINT>=<TERMS>;...]
		for _, fp := range strings.Split(s, ";") {
			fpTerms := strings.Split(fp, "=")
			if len(fpTerms) != 2 {
				fmt.Printf("bad failpoint %q\n", fp)
				os.Exit(1)
			}
			err := Enable(fpTerms[0], fpTerms[1])
			if err != nil {
				fmt.Printf("bad failpoint %s\n", err)
				os.Exit(1)
			}
		}
	}
	if s := os.Getenv("GO_FAILPOINTS_HTTP"); len(s) > 0 {
		if err := serve(s); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}

// Failpoints manages multiple failpoints
type Failpoints struct {
	mu  sync.RWMutex
	reg map[string]*Failpoint
}

// Enable a failpoint on failpath
func (fps *Failpoints) Enable(failpath, inTerms string) error {
	fps.mu.Lock()
	defer fps.mu.Unlock()

	if fps.reg == nil {
		fps.reg = make(map[string]*Failpoint)
	}

	fp := fps.reg[failpath]
	if fp == nil {
		fp = &Failpoint{}
		fps.reg[failpath] = fp
	}
	return fp.Enable(inTerms)
}

// EnableWith enables and locks the failpoint, the lock prevents
// the failpoint to be evaluated. It invokes the action while holding
// the lock. It is useful when enables a panic failpoint
// and does some post actions before the failpoint being evaluated.
func (fps *Failpoints) EnableWith(failpath, inTerms string, action func() error) error {
	fps.mu.Lock()
	defer fps.mu.Unlock()

	if fps.reg == nil {
		fps.reg = make(map[string]*Failpoint)
	}

	fp := fps.reg[failpath]
	if fp == nil {
		fp = &Failpoint{}
		fps.reg[failpath] = fp
	}
	return fp.EnableWith(inTerms, action)
}

// Disable a failpoint on failpath
func (fps *Failpoints) Disable(failpath string) error {
	fps.mu.Lock()
	defer fps.mu.Unlock()

	fp := fps.reg[failpath]
	if fp == nil {
		return ErrDisabled
	}
	return fp.Disable()
}

// Status gives the current setting for the failpoint
func (fps *Failpoints) Status(failpath string) (string, error) {
	fps.mu.RLock()
	fp := fps.reg[failpath]
	fps.mu.RUnlock()
	if fp == nil {
		return "", ErrNotExist
	}
	fp.mu.RLock()
	t := fp.t
	fp.mu.RUnlock()
	if t == nil {
		return "", ErrDisabled
	}
	return t.desc, nil
}

// List returns all the failpoints information
func (fps *Failpoints) List() []string {
	fps.mu.RLock()
	ret := make([]string, 0, len(failpoints.reg))
	for fp := range fps.reg {
		ret = append(ret, fp)
	}
	fps.mu.RUnlock()
	sort.Strings(ret)
	return ret
}

// EvalContext evaluates a failpoint's value, and calls hook if the context is
// not nil and contains hook function. It will return the evaluated value and
// true if the failpoint is active. Always returns false if ctx is nil
// or context does not contains a hook function
func (fps *Failpoints) EvalContext(ctx context.Context, failpath string) (Value, error) {
	if ctx == nil {
		return nil, ErrNoContext
	}
	hook, ok := ctx.Value(failpointCtxKey).(Hook)
	if !ok {
		return nil, ErrNoHook
	}
	if !hook(ctx, failpath) {
		return nil, ErrFiltered
	}
	return fps.Eval(failpath)
}

// Eval evaluates a failpoint's value, It will return the evaluated value and
// true if the failpoint is active
func (fps *Failpoints) Eval(failpath string) (Value, error) {
	fps.mu.RLock()
	fp, found := fps.reg[failpath]
	fps.mu.RUnlock()
	if !found {
		return nil, ErrNotExist
	}

	val, err := fp.Eval()
	if err != nil {
		return nil, fmt.Errorf("%v on %s", err, failpath)
	}
	return val, nil
}

// failpoints is the default
var failpoints Failpoints

// Enable sets a failpoint to a given failpoint description.
func Enable(failpath, inTerms string) error {
	return failpoints.Enable(failpath, inTerms)
}

// EnableWith enables and locks the failpoint, the lock prevents
// the failpoint to be evaluated. It invokes the action while holding
// the lock. It is useful when enables a panic failpoint
// and does some post actions before the failpoint being evaluated.
func EnableWith(failpath, inTerms string, action func() error) error {
	return failpoints.EnableWith(failpath, inTerms, action)
}

// Disable stops a failpoint from firing.
func Disable(failpath string) error {
	return failpoints.Disable(failpath)
}

// Status gives the current setting for the failpoint
func Status(failpath string) (string, error) {
	return failpoints.Status(failpath)
}

// List returns all the failpoints information
func List() []string {
	return failpoints.List()
}

// WithHook binds a hook to a new context which is based on the `ctx` parameter
func WithHook(ctx context.Context, hook Hook) context.Context {
	return context.WithValue(ctx, failpointCtxKey, hook)
}

// EvalContext evaluates a failpoint's value, and calls hook if the context is
// not nil and contains hook function. It will return the evaluated value and
// true if the failpoint is active. Always returns false if ctx is nil
// or context does not contains hook function
func EvalContext(ctx context.Context, failpath string) (Value, error) {
	val, err := failpoints.EvalContext(ctx, failpath)
	// The package level EvalContext usaully be injected into the users
	// code, in which case the error can not be handled by the generated
	// code. We print the error here.
	if err != nil {
		fmt.Println(err)
	}
	return val, err
}

// Eval evaluates a failpoint's value, It will return the evaluated value and
// nil err if the failpoint is active
func Eval(failpath string) (Value, error) {
	val, err := failpoints.Eval(failpath)
	if err != nil {
		if err != ErrDisabled &&
			err != ErrNotExist &&
			err != ErrNoHook &&
			err != ErrNoContext {
			fmt.Println(err)
		}
	}
	return val, err
}
