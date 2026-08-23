package workflow

// prelude is evaluated before every workflow script. It defines the two
// orchestration combinators in JavaScript — they are pure control flow over
// agent(), so implementing them in Go would buy nothing and cost a callback
// bridge — and removes the non-deterministic globals.
//
// Date.now(), Math.random() and argless `new Date()` are disabled on purpose:
// a workflow must replay identically when resumed from its journal, and a
// script that stamps wall-clock time or randomises a prompt cannot. Scripts
// pass timestamps in through args and vary work by item index instead.
const prelude = `
(function () {
  'use strict';

  var RealDate = Date;
  function forbid(what, why) {
    return function () {
      throw new Error(what + ' is unavailable in workflow scripts: ' + why);
    };
  }
  Math.random = forbid('Math.random()', 'it would break resume — vary the prompt or label by index instead');
  RealDate.now = forbid('Date.now()', 'it would break resume — pass timestamps in via args');
  globalThis.Date = new Proxy(RealDate, {
    construct: function (target, argsList, newTarget) {
      if (argsList.length === 0) {
        throw new Error('new Date() is unavailable in workflow scripts: it would break resume — pass timestamps in via args');
      }
      return Reflect.construct(target, argsList, newTarget);
    },
    apply: function (target, thisArg, argsList) {
      if (argsList.length === 0) {
        throw new Error('Date() is unavailable in workflow scripts: it would break resume — pass timestamps in via args');
      }
      return Reflect.apply(target, thisArg, argsList);
    }
  });

  function checkItems(kind, items) {
    if (!Array.isArray(items)) {
      throw new TypeError(kind + '() takes an array as its first argument');
    }
    if (items.length > __wfMaxItems) {
      throw new RangeError(kind + '() got ' + items.length + ' items; the limit is ' + __wfMaxItems);
    }
  }

  // parallel(thunks) — run every thunk concurrently and wait for all of them.
  // A thunk that throws (or whose agent fails) resolves to null rather than
  // rejecting the whole call, so one dead branch never loses the other
  // results; callers filter with .filter(Boolean).
  globalThis.parallel = function parallel(thunks) {
    checkItems('parallel', thunks);
    return Promise.all(thunks.map(function (thunk, i) {
      try {
        var value = typeof thunk === 'function' ? thunk(i) : thunk;
        return Promise.resolve(value).catch(function () { return null; });
      } catch (err) {
        return Promise.resolve(null);
      }
    }));
  };

  // pipeline(items, ...stages) — push each item through every stage
  // independently. There is no barrier between stages: item A can be in stage
  // 3 while item B is still in stage 1, so the wall clock is the slowest
  // single chain rather than the sum of the slowest stage in each round.
  // Every stage receives (prevResult, originalItem, index). A stage that
  // throws drops that item to null and skips its remaining stages.
  globalThis.pipeline = function pipeline(items) {
    checkItems('pipeline', items);
    var stages = Array.prototype.slice.call(arguments, 1);
    return Promise.all(items.map(function (item, i) {
      var chain = Promise.resolve(item);
      stages.forEach(function (stage) {
        chain = chain.then(function (prev) { return stage(prev, item, i); });
      });
      return chain.catch(function () { return null; });
    }));
  };
})();
`
