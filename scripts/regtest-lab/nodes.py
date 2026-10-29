"""Fixed identities and localhost endpoints for the developer-only lab."""
import re
import sys

NODES = {
    'alice': {'alias': 'LOS-Regtest-Alice', 'grpc': 10009, 'rest': 8080, 'p2p': 9735, 'ui': 8443},
    'bob': {'alias': 'LOS-Regtest-Bob', 'grpc': 11009, 'rest': 8180, 'p2p': 9835, 'ui': 8543},
    'charlie': {'alias': 'LOS-Regtest-Charlie', 'grpc': 12009, 'rest': 8280, 'p2p': 9935, 'ui': 8643},
}
CHANNEL_PAIRS = (('alice', 'bob'), ('bob', 'charlie'), ('charlie', 'alice'))


def migrate_alias(text, node):
    """Replace only our old defaults; preserve an operator's custom alias."""
    desired = NODES[node]['alias']
    legacy = {node, node.capitalize(), 'LOS-REGTEST-' + node.upper(), desired}
    lines = text.splitlines(keepends=True)
    start = None
    aliases = []
    for index, line in enumerate(lines):
        section = re.fullmatch(r'\s*\[([^]]+)\]\s*', line.strip())
        if section:
            if start is not None:
                break
            if section[1].strip().lower() == 'application options':
                start = index
        elif start is not None and re.match(r'^\s*alias\s*=', line, re.I):
            aliases.append(index)
    if len(aliases) > 1:
        raise RuntimeError('Duplicate LND aliases; inspect configuration before retrying')
    if aliases:
        index = aliases[0]
        value = lines[index].split('=', 1)[1].strip()
        if value not in legacy:
            return text
        newline = '\r\n' if lines[index].endswith('\r\n') else '\n'
        lines[index] = 'alias=' + desired + newline
        return ''.join(lines)
    if start is None:
        return '[Application Options]\nalias=' + desired + '\n' + text
    lines.insert(start + 1, 'alias=' + desired + '\n')
    return ''.join(lines)


def configured_alias(text):
    application = False
    values = []
    for line in text.splitlines():
        section = re.fullmatch(r'\s*\[([^]]+)\]\s*', line.strip())
        if section:
            application = section[1].strip().lower() == 'application options'
        elif application and re.match(r'^\s*alias\s*=', line, re.I):
            values.append(line.split('=', 1)[1].strip())
    if len(values) != 1 or not values[0] or len(values[0].encode('utf-8')) > 32:
        raise RuntimeError('A single valid LND alias is required')
    return values[0]


if __name__ == '__main__':
    if sys.argv[1:] != ['names']:
        raise SystemExit('Usage: nodes.py names')
    print('\n'.join(NODES))
