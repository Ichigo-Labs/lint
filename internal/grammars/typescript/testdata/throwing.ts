export function check(x: unknown) {
  if (x === null) throw "null input";
  if (typeof x !== "number") throw new TypeError("not a number");
  if (x == 0) throw "zero";
  return x;
}
