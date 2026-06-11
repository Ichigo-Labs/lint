export function label(n: number): string {
  return n > 0 ? "pos" : n < 0 ? "neg" : "zero";
}
const deep = a ? (b ? (c ? (d ? "abcd" : "abc") : "ab") : "a") : "";
