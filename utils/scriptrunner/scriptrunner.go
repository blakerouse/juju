// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package scriptrunner

import (
	"fmt"
	"time"

	"github.com/juju/clock"
	"github.com/juju/errors"
	"github.com/juju/utils/exec"
)

type ScriptResult struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

func RunCommand(command string, environ []string, clock clock.Clock, timeout time.Duration) (*ScriptResult, error) {
	cmd := exec.RunParams{
		Commands:    command,
		Environment: environ,
		Clock:       clock,
	}

	err := cmd.Run()
	if err != nil {
		return nil, errors.Trace(err)
	}

	var cancel chan struct{}

	if timeout != 0 {
		cancel = make(chan struct{})
		go func() {
			<-clock.After(timeout)
			close(cancel)
		}()
	}

	result, err := cmd.WaitWithCancel(cancel)

	if err != nil {
		fmt.Printf("RunCommand: command -> %q, err -> %#v", command, err)
		return &ScriptResult{}, errors.Trace(err)
	}
	// fmt.Printf("RunCommand: command -> %q, result -> %#v", command, result)
	// panic here, coz result is nil if error is timeout!!!!!
	return &ScriptResult{
		Stdout: result.Stdout,
		Stderr: result.Stderr,
		Code:   result.Code,
	}, err
}
