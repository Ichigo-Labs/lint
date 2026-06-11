pub fn totals(rows: Vec<&str>) -> i64 {
    rows.iter()
        .filter(|r| !r.is_empty())
        .map(|r| r.parse::<i64>().unwrap())
        .sum()
}

pub fn outer() -> i64 {
    let v = std::env::var("N").unwrap();
    v.parse().unwrap_or(0)
}
