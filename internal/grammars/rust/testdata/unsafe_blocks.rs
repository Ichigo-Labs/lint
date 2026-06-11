pub unsafe fn poke(ptr: *mut u8) {
    unsafe {
        unsafe {
            *ptr = 1;
        }
    }
}

pub fn reinterpret(x: f32) -> u32 {
    unsafe { std::mem::transmute(x) }
}

pub fn reinterpret2(x: f32) -> u32 {
    unsafe { mem::transmute(x) }
}
