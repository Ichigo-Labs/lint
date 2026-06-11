pub fn run(items: &[String]) -> usize {
    let count = items.iter().map(|x| x.parse::<i32>().unwrap()).count();
    eprintln!("parsed {count}");
    count
}

pub fn todo_branch(flag: bool) -> i32 {
    if flag {
        todo!()
    } else {
        unimplemented!()
    }
}
