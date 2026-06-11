#pragma once
#include <memory>

template <typename T>
class Pool {
    T *raw_ = nullptr;
public:
    T *get() {
        if (raw_ = allocate()) {
            return raw_;
        }
        return nullptr;
    }
    T *allocate();
};
