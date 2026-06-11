void dispatch(int x) {
    switch (x) {
        case 1: a(); break;
        case 2: b(); break;
    }
}

void total(int x) {
    switch (x) {
        case 1: a(); break;
        default: c();
    }
}
