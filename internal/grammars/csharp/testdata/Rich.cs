// A deliberately syntax-dense sample so the equivalence test pushes the
// grammar through many parse states: generics, LINQ, async/await, patterns,
// records, interpolated and verbatim strings, nullable types, attributes.
using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;

namespace Lint.Testdata
{
    public record Point(double X, double Y)
    {
        public double Magnitude => Math.Sqrt(X * X + Y * Y);
    }

    public interface IRepo<T> where T : class, new()
    {
        Task<IReadOnlyList<T>> AllAsync(CancellationToken ct = default);
    }

    [Obsolete("use NewThing")]
    public sealed class Thing<TKey, TValue> : IDisposable
        where TKey : notnull
    {
        private readonly Dictionary<TKey, TValue?> _map = new();
        public event EventHandler? Changed;

        public TValue? this[TKey key]
        {
            get => _map.TryGetValue(key, out var v) ? v : default;
            set { _map[key] = value; Changed?.Invoke(this, EventArgs.Empty); }
        }

        public async Task<int> CountEvensAsync(IEnumerable<int> xs)
        {
            await Task.Yield();
            var query = from x in xs
                        where x % 2 == 0
                        orderby x descending
                        select x * x;
            return query.Count();
        }

        public static string Describe(object o) => o switch
        {
            Point(0, 0) => "origin",
            Point { X: > 0, Y: > 0 } p => $"quadrant I at {p.Magnitude:F2}",
            int n when n < 0 => @"negative
verbatim",
            string s => $@"string of {s.Length}",
            null => "nothing",
            _ => "unknown",
        };

        public void Dispose()
        {
            unsafe
            {
                int local = 42;
                int* p = &local;
                *p += 1;
            }
        }
    }

    file static class Helpers
    {
        public static int Fib(int n) => n <= 1 ? n : Fib(n - 1) + Fib(n - 2);

        public static void Main(string[] args)
        {
            Span<byte> buf = stackalloc byte[16];
            var things = new List<(string Name, int Rank)> { ("a", 1), ("b", 2) };
            foreach (var (name, rank) in things)
            {
                Console.WriteLine($"{name}: {rank switch { 1 => "first", _ => "later" }}");
            }
            var sum = things.Sum(t => t.Rank) is var s && s > 1 ? s : 0;
            Console.WriteLine(sum);
        }
    }
}
