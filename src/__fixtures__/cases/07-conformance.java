@Entity(name = "user", indexes = {@Index("a"), @Index("b")})
sealed class User<@NonNull T> permits Admin, Guest {
  void setName(User<T> this, String name) {}

  Outer.Inner make(Outer Outer.this) {
    return Outer.this.create();
  }

  Object widen(Object o) {
    Runnable r = (Runnable & java.io.Serializable) o;
    for (int i = 0, j = 10; i < j; i++, j--) {
      System.out.println(i);
    }
    return Outer.super.toString();
  }
}

@interface Config {
  int retries() default 3;

  String[] tags() default {"x", "y"};
}
