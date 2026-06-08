class ControlFlow {
	int classify(int n) {
		for (int i = 0; i < n; i++) {
			if (i % 2 == 0) {
				continue;
			}
		}
		try (var r = open()) {
			return r.read();
		} catch (java.io.IOException | RuntimeException e) {
			throw new IllegalStateException(e);
		} finally {
			cleanup();
		}
	}
}
