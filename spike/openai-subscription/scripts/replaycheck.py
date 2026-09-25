#!/usr/bin/env python3
# structural checks for the encrypted-reasoning follow-up. prints counts,
# booleans, item types, and decisions only -- never payloads or identifiers.
import glob, json, os, sys

S = os.path.expanduser('~/.local/state/pane-spike')

def stream_reasoning(raw_path):
    """encrypted_content values observed on the wire, keyed by item id."""
    seen = {}
    for line in open(raw_path):
        try:
            e = json.loads(line)
        except ValueError:
            continue
        items = []
        if e.get('type') == 'response.output_item.done':
            items.append(e.get('item') or {})
        if e.get('type') in ('response.completed', 'response.done'):
            items.extend((e.get('response') or {}).get('output') or [])
        for it in items:
            if it.get('type') == 'reasoning' and it.get('encrypted_content'):
                seen[it['id']] = it['encrypted_content']
    return seen

def envelopes(doc):
    out = []
    for i, m in enumerate(doc['messages']):
        env = m.get('continuation')
        if m['role'] == 'assistant' and env:
            out.append((i, env))
    return out

def phase_a(docpath, raw_paths):
    doc = json.load(open(docpath))
    wire = {}
    for r in raw_paths:
        wire.update(stream_reasoning(r))
    total = 0
    for i, env in envelopes(doc):
        types = [it['type'] for it in env['items']]
        rs = [it for it in env['items'] if it['type'] == 'reasoning']
        nonempty = [it for it in rs if it.get('encrypted_content')]
        total += len(nonempty)
        eq = [wire.get(it['id']) == it['encrypted_content'] for it in nonempty]
        print(f'  message {i}: items={types} reasoning={len(rs)} nonempty_encrypted={len(nonempty)} '
              f'lengths={[len(it["encrypted_content"]) for it in nonempty]} equals_stream={eq}')
    print(f'  total nonempty encrypted reasoning items stored: {total}')
    return total

def phase_b(docpath, summary_path, request_path):
    doc = json.load(open(docpath))
    summ = json.load(open(summary_path))
    body = json.load(open(request_path))
    inp = body['input']
    print('  request input types:', [it.get('type') or 'role:' + it.get('role', '?') for it in inp])
    print('  tools in request:', 'tools' in body, '| store:', body.get('store'), '| include:', body.get('include'))
    print('  replay decisions:', [(d['index'], d['replayed'], d['reason']) for d in summ.get('replay_decisions') or []])
    canon = [json.dumps(it, sort_keys=True) for it in inp]
    for i, env in envelopes(doc):
        if i >= len(doc['messages']) - 1:
            continue  # the reload's own new answer
        items = [json.dumps(it, sort_keys=True) for it in env['items']]
        pos = -1
        for k in range(len(canon) - len(items) + 1):
            if canon[k:k + len(items)] == items:
                pos = k
                break
        rs = [it for it in env['items'] if it['type'] == 'reasoning' and it.get('encrypted_content')]
        sent = {it.get('id'): it.get('encrypted_content') for it in inp if it.get('type') == 'reasoning'}
        eq = [sent.get(it['id']) == it['encrypted_content'] for it in rs]
        print(f'  message {i}: envelope items {[json.loads(x)["type"] for x in items]} transmitted contiguously in order: {pos >= 0} '
              f'(at input index {pos}); encrypted reasoning sent byte-equal: {eq}')
    rounds = summ.get('rounds') or []
    print('  outcome:', [(r['raw_finish'], r['tool_calls']) for r in rounds], '| error:', summ.get('error_kind'))

if __name__ == '__main__':
    mode = sys.argv[1]
    if mode == 'a':
        phase_a(sys.argv[2], sys.argv[3:])
    else:
        phase_b(sys.argv[2], sys.argv[3], sys.argv[4])
