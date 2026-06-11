use std::fmt::Display;

pub struct Wrapper<T: Display> {
    inner: Option<T>,
}

impl<T: Display> Wrapper<T> {
    pub fn show(&self) -> String {
        let v = self.inner.as_ref().expect("empty wrapper");
        format!("{v}")
    }
}
