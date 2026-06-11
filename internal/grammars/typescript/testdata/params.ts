function connect(host, port, user, pass, timeout, retries) {
  return `${user}@${host}:${port}?t=${timeout}&r=${retries}&p=${pass}`;
}
function ok(a, b) { return a + b; }
function gather(first, second, third, fourth, fifth) {
  return [first, second, third, fourth, fifth];
}
