import assert from 'node:assert/strict';
import { test } from 'node:test';
import { filterNodes, nodeListRefreshMs, nodeRate } from '../src/lib/node-list.ts';

test('refresh configuration is bounded and falls back safely', () => {
  for (const value of [undefined, '', '0', '-1', '4999', '300001', 'NaN', 'Infinity', '5000.5']) {
    assert.equal(nodeListRefreshMs(value), 30000);
  }
  for (const value of ['5000', '30000', '60000', '300000']) {
    assert.equal(nodeListRefreshMs(value), Number(value));
  }
});

test('rates preserve administrator permille precision', () => {
  assert.equal(nodeRate(1), '0.001');
  assert.equal(nodeRate(500), '0.5');
  assert.equal(nodeRate(1000), '1');
  assert.equal(nodeRate(1250), '1.25');
  assert.equal(nodeRate(10000), '10');
});

test('search and line filters combine without mutating the catalog', () => {
  const nodes = [
    { id: 'hk', name: '香港 HK-01', region: 'HK', line_type: 'dedicated' },
    { id: 'jp', name: '东京 JP-01', region: 'JP', line_type: 'direct' },
  ];
  assert.deepEqual(filterNodes(nodes, ' hk ', 'all'), [nodes[0]]);
  assert.deepEqual(filterNodes(nodes, '东京', 'direct'), [nodes[1]]);
  assert.deepEqual(filterNodes(nodes, 'jp', 'dedicated'), []);
  assert.deepEqual(filterNodes(nodes, '', 'all'), nodes);
  assert.deepEqual(filterNodes([], '', 'all'), []);
  assert.equal(nodes.length, 2);
});
