package parser

import (
	"fmt"
	"net/url"
	"strconv"

	"skia.googlesource.com/buildbot.git/perf/go/config"
	"skia.googlesource.com/buildbot.git/perf/go/types"
	"skia.googlesource.com/buildbot.git/perf/go/vec"
)

// filterFunc is a Func that returns a filtered set of Traces from the Tile in
// the Context.
//
// It expects a single argument that is a string in URL query format, ala
// os=Ubuntu12&config=8888.
func filterFunc(ctx *Context, node *Node) ([]*types.Trace, error) {
	if len(node.Args) != 1 {
		return nil, fmt.Errorf("filter() takes a single argument.")
	}
	if node.Args[0].Typ != NodeString {
		return nil, fmt.Errorf("filter() takes a string argument.")
	}
	query, err := url.ParseQuery(node.Args[0].Val)
	if err != nil {
		return nil, fmt.Errorf("filter() arg not a valid URL query parameter: %s", err)
	}
	traces := []*types.Trace{}
	for _, tr := range ctx.Tile.Traces {
		if traceMatches(tr, query) {
			traces = append(traces, tr.DeepCopy())
		}
	}
	return traces, nil
}

// normFunc implements Func and normalizes the traces to a mean of 0 and a
// standard deviation of 1.0. If a second optional number is passed in to
// norm() then that is used as the minimum standard deviation that is
// normalized, otherwise it defaults to config.MIN_STDDEV.
func normFunc(ctx *Context, node *Node) ([]*types.Trace, error) {
	if len(node.Args) > 2 || len(node.Args) == 0 {
		return nil, fmt.Errorf("norm() takes one or two arguments.")
	}
	if node.Args[0].Typ != NodeFunc {
		return nil, fmt.Errorf("norm() takes a function as its first argument.")
	}
	minStdDev := config.MIN_STDDEV
	if len(node.Args) == 2 {
		if node.Args[1].Typ != NodeNum {
			return nil, fmt.Errorf("norm() takes a number as its second argument.")
		}
		var err error
		minStdDev, err = strconv.ParseFloat(node.Args[1].Val, 64)
		if err != nil {
			return nil, fmt.Errorf("norm() stddev not a valid number %s : %s", node.Args[1].Val, err)
		}
	}
	traces, err := node.Args[0].Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("norm() failed evaluating argument: %s", err)
	}

	for _, tr := range traces {
		vec.Norm(tr.Values, minStdDev)
	}

	return traces, nil
}

// fillFunc implements Func and fills in all the missing datapoints with nearby
// points.
//
// Note that a Trace with all MISSING_DATA_SENTINEL values will be filled with
// 0's.
func fillFunc(ctx *Context, node *Node) ([]*types.Trace, error) {
	if len(node.Args) != 1 {
		return nil, fmt.Errorf("fill() takes a single argument.")
	}
	if node.Args[0].Typ != NodeFunc {
		return nil, fmt.Errorf("fill() takes a function argument.")
	}
	traces, err := node.Args[0].Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("fill() failed evaluating argument: %s", err)
	}

	for _, tr := range traces {
		vec.Fill(tr.Values)
	}
	return traces, nil
}

// aveFunc implements Func and averages the values of all argument
// traces into a single trace.
//
// MISSING_DATA_SENTINEL values are not included in the average.  Note that if
// all the values at an index are MISSING_DATA_SENTINEL then the average will
// be MISSING_DATA_SENTINEL.
func aveFunc(ctx *Context, node *Node) ([]*types.Trace, error) {
	if len(node.Args) != 1 {
		return nil, fmt.Errorf("ave() takes a single argument.")
	}
	if node.Args[0].Typ != NodeFunc {
		return nil, fmt.Errorf("ave() takes a function argument.")
	}
	traces, err := node.Args[0].Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("ave() argument failed to evaluate: %s", err)
	}

	if len(traces) == 0 {
		return traces, nil
	}

	ret := types.NewTraceN(len(traces[0].Values))
	for i, _ := range ret.Values {
		sum := 0.0
		count := 0
		for _, tr := range traces {
			if v := tr.Values[i]; v != config.MISSING_DATA_SENTINEL {
				sum += v
				count += 1
			}
		}
		if count > 0 {
			ret.Values[i] = sum / float64(count)
		}
	}
	return []*types.Trace{ret}, nil
}