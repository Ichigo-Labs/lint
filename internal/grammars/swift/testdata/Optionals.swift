struct User { var name: String? }

func display(user: User?) {
    let name = user!.name!
    print("héllo 🎉", name)
}

func safe(user: User?) {
    if let name = user?.name {
        log(name)
    }
}
