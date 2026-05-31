class Box<T extends Comparable<T>> {
	private T value;

	<R> R map(java.util.function.Function<? super T, ? extends R> f) {
		return f.apply(value);
	}

	java.util.Map<String, java.util.List<Integer>> table;
}
