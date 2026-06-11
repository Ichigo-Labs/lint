class Handlers {
    async void FireAndForget() {
        await Work();
    }

    async Task Proper() {
        await Work();
    }
}
