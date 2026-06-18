open module com.acme.app {
	requires java.base;
	requires transitive java.sql;
	exports com.acme.api;
	exports com.acme.internal to com.acme.test;
	uses com.acme.spi.Service;
	provides com.acme.spi.Service with com.acme.impl.ServiceImpl;
}
