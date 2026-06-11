type Result<T, E = Error> = { ok: true; value: T } | { ok: false; error: E };
function unwrap<T>(r: Result<T>): T {
  if (!r.ok) throw "unwrapped an error result";
  return r.value;
}
const pair = <A, B>(a: A, b: B): [A, B] => [a, b];
function keys<T extends object, K extends keyof T>(o: T, ...ks: K[]): K[] {
  return ks.filter((k) => k in o);
}
