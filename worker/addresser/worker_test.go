// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package addresser_test

import (
	"fmt"
	"time"

	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/api"
	apiaddresser "github.com/juju/juju/api/addresser"
	"github.com/juju/juju/feature"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/juju/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/provider/common"
	"github.com/juju/juju/provider/dummy"
	"github.com/juju/juju/state"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/worker"
	"github.com/juju/juju/worker/addresser"
)

type workerSuite struct {
	testing.JujuConnSuite
	machine  *state.Machine
	machine2 *state.Machine

	apiSt *api.State
	api   *apiaddresser.API
}

var _ = gc.Suite(&workerSuite{})

func (s *workerSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)
	s.SetFeatureFlags(feature.AddressAllocation)
	// Unbreak dummy provider methods.
	s.AssertConfigParameterUpdated(c, "broken", "")

	s.apiSt, _ = s.OpenAPIAsNewMachine(c, state.JobManageEnviron)
	s.api = s.apiSt.Addresser()

	machine, err := s.State.AddMachine("quantal", state.JobHostUnits)
	s.machine = machine
	c.Assert(err, jc.ErrorIsNil)
	err = s.machine.SetProvisioned("foo", "fake_nonce", nil)
	c.Assert(err, jc.ErrorIsNil)

	// this machine will be destroyed after address creation to test the
	// handling of addresses for machines that have gone.
	machine2, err := s.State.AddMachine("quantal", state.JobHostUnits)
	s.machine2 = machine2
	c.Assert(err, jc.ErrorIsNil)

	s.createAddresses(c)
	s.State.StartSync()
}

func (s *workerSuite) createAddresses(c *gc.C) {
	addresses := []string{
		"0.1.2.3", "0.1.2.4", "0.1.2.5", "0.1.2.6",
	}
	for i, rawAddr := range addresses {
		addr := network.NewAddress(rawAddr)
		ipAddr, err := s.State.AddIPAddress(addr, "foobar")
		c.Assert(err, jc.ErrorIsNil)
		if i%2 == 1 {
			err = ipAddr.AllocateTo(s.machine2.Id(), "wobble", "")
		} else {
			err = ipAddr.AllocateTo(s.machine.Id(), "wobble", "")
			c.Assert(err, jc.ErrorIsNil)
		}
	}
	// Two of the addresses start out allocated to this
	// machine which we destroy to test the handling of
	// addresses allocated to dead machines.
	err := s.machine2.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	err = s.machine2.Remove()
	c.Assert(err, jc.ErrorIsNil)
}

func dummyListen() chan dummy.Operation {
	opsChan := make(chan dummy.Operation, 25)
	dummy.Listen(opsChan)
	return opsChan
}

func (s *workerSuite) waitForInitialDead(c *gc.C) {
	for a := common.ShortAttempt.Start(); a.Next(); {
		dead, err := s.State.DeadIPAddresses()
		c.Assert(err, jc.ErrorIsNil)
		if len(dead) == 0 {
			break
		}
		if !a.HasNext() {
			c.Fatalf("timeout waiting for initial change (dead: %#v)", dead)
		}
	}
}

func waitForReleaseOp(c *gc.C, opsChan chan dummy.Operation) dummy.OpReleaseAddress {
	var releaseOp dummy.OpReleaseAddress
	var ok bool
	select {
	case op := <-opsChan:
		releaseOp, ok = op.(dummy.OpReleaseAddress)
		c.Assert(ok, jc.IsTrue)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout while expecting operation")
	}
	return releaseOp
}

func makeReleaseOp(digit int) dummy.OpReleaseAddress {
	return dummy.OpReleaseAddress{
		Env:        "dummyenv",
		InstanceId: "foo",
		SubnetId:   "foobar",
		Address:    network.NewAddress(fmt.Sprintf("0.1.2.%d", digit)),
	}
}

func (s *workerSuite) assertStop(c *gc.C, w worker.Worker) {
	c.Assert(worker.Stop(w), jc.ErrorIsNil)
}

func (s *workerSuite) TestWorkerReleasesAlreadyDead(c *gc.C) {
	// we start with two dead addresses
	dead, err := s.State.DeadIPAddresses()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(dead, gc.HasLen, 2)

	opsChan := dummyListen()

	_, stop := s.newWorker(c)
	defer stop()
	s.waitForInitialDead(c)

	op1 := waitForReleaseOp(c, opsChan)
	op2 := waitForReleaseOp(c, opsChan)
	expected := []dummy.OpReleaseAddress{makeReleaseOp(4), makeReleaseOp(6)}

	// The machines are dead, so ReleaseAddress should be called with
	// instance.UnknownId.
	expected[0].InstanceId = instance.UnknownId
	expected[1].InstanceId = instance.UnknownId
	c.Assert([]dummy.OpReleaseAddress{op1, op2}, jc.SameContents, expected)
}

func (s *workerSuite) TestWorkerIgnoresAliveAddresses(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()
	s.waitForInitialDead(c)

	// Add a new alive address.
	addr := network.NewAddress("0.1.2.9")
	ipAddr, err := s.State.AddIPAddress(addr, "foobar")
	c.Assert(err, jc.ErrorIsNil)
	err = ipAddr.AllocateTo(s.machine.Id(), "wobble", "")
	c.Assert(err, jc.ErrorIsNil)

	// The worker must not kill this address.
	for a := common.ShortAttempt.Start(); a.Next(); {
		ipAddr, err := s.State.IPAddress("0.1.2.9")
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(ipAddr.Life(), gc.Equals, state.Alive)
	}
}

func (s *workerSuite) TestWorkerRemovesDeadAddress(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()
	s.waitForInitialDead(c)
	opsChan := dummyListen()

	addr, err := s.State.IPAddress("0.1.2.3")
	c.Assert(err, jc.ErrorIsNil)
	err = addr.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)

	// Wait for ReleaseAddress attempt.
	op := waitForReleaseOp(c, opsChan)
	c.Assert(op, jc.DeepEquals, makeReleaseOp(3))

	// The address should have been removed from state.
	for a := common.ShortAttempt.Start(); a.Next(); {
		_, err := s.State.IPAddress("0.1.2.3")
		if errors.IsNotFound(err) {
			break
		}
		if !a.HasNext() {
			c.Fatalf("IP address not removed")
		}
	}
}

func (s *workerSuite) TestWorkerAcceptsBrokenRelease(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()
	s.waitForInitialDead(c)

	s.AssertConfigParameterUpdated(c, "broken", "ReleaseAddress")

	ipAddr, err := s.State.IPAddress("0.1.2.3")
	c.Assert(err, jc.ErrorIsNil)
	err = ipAddr.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)

	// The address should stay in state.
	ipAddr, err = s.State.IPAddress("0.1.2.3")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ipAddr.Life(), gc.Equals, state.Dead)

	// Makre ReleaseAddress work again, it must be cleaned up then.
	s.AssertConfigParameterUpdated(c, "broken", "")

	for a := common.ShortAttempt.Start(); a.Next(); {
		_, err := s.State.IPAddress("0.1.2.3")
		if errors.IsNotFound(err) {
			break
		}
		if !a.HasNext() {
			c.Fatalf("IP address not removed")
		}
	}
}

func (s *workerSuite) TestMachineRemovalTriggersWorker(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()
	s.waitForInitialDead(c)
	opsChan := dummyListen()

	// Add special test machine.
	machine, err := s.State.AddMachine("quantal", state.JobHostUnits)
	c.Assert(err, jc.ErrorIsNil)
	err = machine.SetProvisioned("foo", "really-fake", nil)
	c.Assert(err, jc.ErrorIsNil)

	// Add a new alive address.
	addr := network.NewAddress("0.1.2.9")
	ipAddr, err := s.State.AddIPAddress(addr, "foobar")
	c.Assert(err, jc.ErrorIsNil)
	err = ipAddr.AllocateTo(machine.Id(), "foo", "")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ipAddr.InstanceId(), gc.Equals, instance.Id("foo"))

	// Wait some time and remove test machine again.
	for a := common.ShortAttempt.Start(); a.Next(); {
		err = ipAddr.Refresh()
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(ipAddr.Life(), gc.Equals, state.Alive)
	}

	err = machine.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	err = machine.Remove()
	c.Assert(err, jc.ErrorIsNil)

	err = ipAddr.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ipAddr.Life(), gc.Equals, state.Dead)

	// Wait for ReleaseAddress attempt.
	op := waitForReleaseOp(c, opsChan)
	c.Assert(op, jc.DeepEquals, makeReleaseOp(9))

	// The address should have been removed from state.
	for a := common.ShortAttempt.Start(); a.Next(); {
		_, err := s.State.IPAddress("0.1.2.9")
		if errors.IsNotFound(err) {
			break
		}
		if !a.HasNext() {
			c.Fatalf("IP address not removed")
		}
	}
}

func (s *workerSuite) newWorker(c *gc.C) (worker.Worker, func()) {
	w, err := addresser.NewWorker(s.api)
	c.Assert(err, jc.ErrorIsNil)
	stop := func() {
		worker.Stop(w)
	}
	return w, stop
}

type workerDisabledSuite struct {
	testing.JujuConnSuite
	machine *state.Machine

	apiSt *api.State
	api   *apiaddresser.API
}

var _ = gc.Suite(&workerDisabledSuite{})

func (s *workerDisabledSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)
	// Unbreak dummy provider methods.
	s.AssertConfigParameterUpdated(c, "broken", "")

	s.apiSt, _ = s.OpenAPIAsNewMachine(c, state.JobManageEnviron)
	s.api = s.apiSt.Addresser()

	// Create a machine.
	machine, err := s.State.AddMachine("quantal", state.JobHostUnits)
	s.machine = machine
	c.Assert(err, jc.ErrorIsNil)
	err = s.machine.SetProvisioned("foo", "fake_nonce", nil)
	c.Assert(err, jc.ErrorIsNil)

	// Create address and assign to machine.
	addr := network.NewAddress("0.1.2.3")
	ipAddr, err := s.State.AddIPAddress(addr, "foobar")
	c.Assert(err, jc.ErrorIsNil)
	err = ipAddr.AllocateTo(s.machine.Id(), "wobble", "")
	c.Assert(err, jc.ErrorIsNil)

	s.State.StartSync()
}

func (s *workerDisabledSuite) TestWorkerIgnoresAliveAddresses(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()

	// Add a new alive address.
	addr := network.NewAddress("0.1.2.9")
	ipAddr, err := s.State.AddIPAddress(addr, "foobar")
	c.Assert(err, jc.ErrorIsNil)
	err = ipAddr.AllocateTo(s.machine.Id(), "wobble", "")
	c.Assert(err, jc.ErrorIsNil)

	// The worker must not kill this address.
	for a := common.ShortAttempt.Start(); a.Next(); {
		ipAddr, err := s.State.IPAddress("0.1.2.9")
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(ipAddr.Life(), gc.Equals, state.Alive)
	}
}

func (s *workerDisabledSuite) TestWorkerIgnoresDeadAddresses(c *gc.C) {
	_, stop := s.newWorker(c)
	defer stop()

	// Remove machine with addresses.
	err := s.machine.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	err = s.machine.Remove()
	c.Assert(err, jc.ErrorIsNil)

	// The worker must not remove this address.
	for a := common.ShortAttempt.Start(); a.Next(); {
		ipAddr, err := s.State.IPAddress("0.1.2.3")
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(ipAddr.Life(), gc.Equals, state.Dead)
	}
}

func (s *workerDisabledSuite) newWorker(c *gc.C) (worker.Worker, func()) {
	w, err := addresser.NewWorker(s.api)
	c.Assert(err, jc.ErrorIsNil)
	stop := func() {
		worker.Stop(w)
	}
	return w, stop
}
